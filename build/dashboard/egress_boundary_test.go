package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Egress boundary: docs/tor-boundary.md. Only tor_http.go may open a connection to a non-stack address.
var egressSymbols = map[string]map[string]bool{
	"net": setOf("Dial", "DialTimeout", "DialTCP", "DialUDP", "DialIP", "DialUnix", "Dialer", "Resolver", "DefaultResolver",
		"LookupHost", "LookupIP", "LookupAddr", "LookupCNAME", "LookupMX", "LookupNS", "LookupPort", "LookupSRV", "LookupTXT"),
	"net/http":                  setOf("Get", "Post", "PostForm", "Head", "DefaultClient", "DefaultTransport", "Client", "Transport", "ProxyFromEnvironment", "ProxyURL"),
	"crypto/tls":                setOf("Dial", "DialWithDialer", "Dialer"),
	"google.golang.org/grpc":    setOf("Dial", "DialContext", "NewClient"),
	"os/exec":                   setOf("Command", "CommandContext"),
	"net/http/httputil":         setOf("ReverseProxy", "NewSingleHostReverseProxy"),
	"golang.org/x/net/http2":    setOf("Transport"),
	"golang.org/x/net/proxy":    setOf("SOCKS5", "FromEnvironment", "Direct"),
	"golang.org/x/net/internal": setOf(),
	"syscall":                   setOf("Socket", "Connect", "Bind", "Listen", "Accept", "Sendto", "Sendmsg", "Recvfrom", "Syscall", "Syscall6", "RawSyscall", "RawSyscall6"),
}

var bannedImports = []string{"net/smtp", "net/rpc", "net/rpc/jsonrpc", "net/textproto", "golang.org/x/net/proxy", "golang.org/x/net/websocket", "golang.org/x/net/http2", "golang.org/x/sys/unix", "golang.org/x/sys/windows"}

// Each entry is "file symbol": uses, with the reason it stays inside the boundary.
var allowedEgress = map[string]struct {
	count  int
	reason string
}{
	"background.go http.Client":               {2, "RPC client whose URL validateInternalHTTPURL restricts to the stack network"},
	"chain_fees.go http.Client":               {1, "RPC client whose URL validateInternalHTTPURL restricts to the stack network"},
	"chains.go http.Client":                   {2, "RPC client whose URL validateInternalHTTPURL restricts to the stack network"},
	"dashboard.go http.Client":                {2, "RPC client whose URL validateInternalHTTPURL restricts to the stack network"},
	"family_watch.go http.Client":             {1, "RPC client whose URL validateInternalHTTPURL restricts to the stack network"},
	"family_watch_dogecoin.go http.Client":    {1, "RPC client whose URL validateInternalHTTPURL restricts to the stack network"},
	"http.go http.Client":                     {1, "RPC client whose URL validateInternalHTTPURL restricts to the stack network"},
	"main.go http.Client":                     {2, "container loopback health check and the Bitcoin RPC client on the stack network"},
	"monero.go http.Client":                   {1, "RPC client whose URL validateInternalHTTPURL restricts to the stack network"},
	"monero_fees.go http.Client":              {1, "RPC client whose URL validateInternalHTTPURL restricts to the stack network"},
	"monero_watch.go http.Client":             {2, "RPC client whose URL validateInternalHTTPURL restricts to the stack network"},
	"utxo_manager.go http.Client":             {2, "RPC client whose URL validateInternalHTTPURL restricts to the stack network"},
	"zcash_proxy.go http.Client":              {1, "RPC client whose URL validateInternalHTTPURL restricts to the stack network"},
	"tari-wallet/launcher.go net.DialTimeout": {1, "readiness probe of the wallet gRPC port at 127.0.0.1:18143"},
	"tari-wallet/launcher.go exec.Command":    {1, "starts the pinned minotari_console_wallet binary, no shell"},
	"tari.go grpc.NewClient":                  {1, "gRPC channel to the validated stack-internal Tari node"},
	"tari_watch.go grpc.NewClient":            {1, "gRPC channel to the validated stack-internal Tari wallet"},
	"tor_http.go http.Client":                 {1, "the Tor client: SOCKS5 transport, no redirects"},
	"tor_http.go http.Transport":              {1, "the Tor client transport: Proxy nil, dials only the SOCKS5 proxy"},
	"tor_http.go net.Dialer":                  {1, "connects only to the Tor SOCKS5 proxy address"},
}

// Hosts that may appear in a URL literal. Everything outside the stack subnet goes through the Tor clients.
var allowedURLHosts = map[string]string{
	"172.29.0.26":             "bitcoin container on the internal network",
	"172.29.0.30":             "monero container on the internal network",
	"172.29.0.31":             "monero wallet container on the internal network",
	"172.29.0.32":             "tari container on the internal network",
	"172.29.0.33":             "litecoin container on the internal network",
	"172.29.0.34":             "dogecoin container on the internal network",
	"172.29.0.35":             "bitcoin cash container on the internal network",
	"172.29.0.36":             "zcash container on the internal network",
	"172.29.0.37":             "tari wallet container on the internal network",
	"127.0.0.1":               "container loopback health check",
	"api.telegram.org":        "alert delivery through the Tor client",
	"api.coingecko.com":       "price lookup through the Tor client",
	"api.github.com":          "release check through the Tor client",
	"www.linuxmint.com":       "display-only watch-wallet example source link, never fetched",
	"bitcoincashnode.org":     "display-only watch-wallet example source link, never fetched",
	"foundation.dogecoin.com": "display-only watch-wallet example source link, never fetched",
	"zips.z.cash":             "display-only watch-wallet example source link, never fetched",
	"www.getmonero.org":       "display-only watch-wallet example source link, never fetched",
	"github.com":              "project URL inside the price-lookup User-Agent header, sent through the Tor client",
}

var constructedTypes = setOf("Client", "Transport", "Dialer", "Resolver", "ReverseProxy")

func setOf(names ...string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

func scanEgress(t *testing.T, sources map[string]string) (uses map[string]int, hosts map[string]int, banned []string) {
	t.Helper()
	uses, hosts = map[string]int{}, map[string]int{}
	urlHost := regexp.MustCompile(`https?://([^/\s"'?:]+)`)
	fset := token.NewFileSet()
	for name, source := range sources {
		file, err := parser.ParseFile(fset, name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		local := map[string]string{}
		for _, spec := range file.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			if spec.Name != nil && spec.Name.Name == "." {
				banned = append(banned, name+" dot-imports "+path)
			}
			for _, bad := range bannedImports {
				if path == bad {
					banned = append(banned, name+" imports "+path)
				}
			}
			alias := filepath.Base(path)
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			local[alias] = path
		}
		record := func(expr ast.Expr) {
			for {
				switch t := expr.(type) {
				case *ast.ArrayType:
					expr = t.Elt
					continue
				case *ast.MapType:
					expr = t.Value
					continue
				}
				break
			}
			if sel, ok := expr.(*ast.SelectorExpr); ok {
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Obj == nil && egressSymbols[local[ident.Name]][sel.Sel.Name] {
					uses[name+" "+ident.Name+"."+sel.Sel.Name]++
				}
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.CompositeLit:
				record(n.Type)
			case *ast.ValueSpec:
				record(n.Type)
			case *ast.Field:
				record(n.Type)
			case *ast.KeyValueExpr:
				if key, ok := n.Key.(*ast.Ident); ok && key.Name == "Host" {
					if lit, ok := n.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						value, _ := strconv.Unquote(lit.Value)
						hosts[name+" "+value]++
					}
				}
			case *ast.CallExpr:
				if fn, ok := n.Fun.(*ast.Ident); ok && (fn.Name == "new" || fn.Name == "make") && len(n.Args) >= 1 {
					record(n.Args[0])
				}
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "JoinHostPort" && len(n.Args) == 2 {
					if lit, ok := n.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						value, _ := strconv.Unquote(lit.Value)
						hosts[name+" "+value]++
					}
				}
			case *ast.SelectorExpr:
				if ident, ok := n.X.(*ast.Ident); ok && ident.Obj == nil && !constructedTypes[n.Sel.Name] && egressSymbols[local[ident.Name]][n.Sel.Name] {
					uses[name+" "+ident.Name+"."+n.Sel.Name]++
				}
			case *ast.BasicLit:
				if n.Kind == token.STRING {
					value, _ := strconv.Unquote(n.Value)
					for _, match := range urlHost.FindAllStringSubmatch(value, -1) {
						hosts[name+" "+match[1]]++
					}
				}
			}
			return true
		})
	}
	return uses, hosts, banned
}

func dashboardSources(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || strings.HasSuffix(file, ".pb.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		sources[file] = string(source)
	}
	if len(sources) < 20 {
		t.Fatalf("scanned only %d dashboard sources", len(sources))
	}
	launcher, err := os.ReadFile(filepath.Join("..", "tari-wallet", "launcher.go"))
	if err != nil {
		t.Fatal(err)
	}
	sources["tari-wallet/launcher.go"] = string(launcher)
	return sources
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func TestEgressScannerFlagsDirectNetworkUse(t *testing.T) {
	uses, hosts, banned := scanEgress(t, map[string]string{
		"a.go": `package main
import (
	"net"
	"net/http"
	h "net/http"
	"net/smtp"
	"os/exec"
)
func f() {
	net.Dial("tcp", "1.2.3.4:1")
	http.Get("https://evil.example/x")
	_ = &h.Client{}
	_ = h.DefaultTransport
	net.LookupHost("example.com")
	exec.Command("curl")
	var net struct{ Dial func() }
	net.Dial()
}`,
	})
	for _, want := range []string{"a.go net.Dial", "a.go http.Get", "a.go h.Client", "a.go h.DefaultTransport", "a.go net.LookupHost", "a.go exec.Command"} {
		if uses[want] != 1 {
			t.Errorf("%s not flagged: %v", want, uses)
		}
	}
	if uses["a.go net.Dial"] != 1 {
		t.Errorf("a shadowed local named net was counted: %v", uses)
	}
	if hosts["a.go evil.example"] != 1 || len(banned) != 1 {
		t.Errorf("hosts=%v banned=%v", hosts, banned)
	}
}

func TestEgressScannerFlagsIndirectNetworkUse(t *testing.T) {
	uses, hosts, banned := scanEgress(t, map[string]string{
		"b.go": `package main
import (
	. "net/http"
	"golang.org/x/sys/unix"
	"net"
	"net/http"
	"net/url"
	"syscall"
)
type holder struct {
	c  http.Client
	cs []http.Transport
	p  *http.Client
}
func f() {
	_ = make([]http.Client, 1)
	_ = url.URL{Host: "evil.example"}
	_ = net.JoinHostPort("evil2.example", "80")
	_ = net.JoinHostPort(variable, "80")
	syscall.Socket(2, 1, 0)
	syscall.Connect(1, nil)
	syscall.Statfs("/", nil)
}`,
	})
	for _, want := range []string{"b.go http.Client", "b.go http.Transport", "b.go syscall.Socket", "b.go syscall.Connect"} {
		if uses[want] == 0 {
			t.Errorf("%s not flagged: %v", want, uses)
		}
	}
	if uses["b.go http.Client"] != 2 || uses["b.go syscall.Statfs"] != 0 {
		t.Errorf("field, pointer field or make miscounted: %v", uses)
	}
	if hosts["b.go evil.example"] != 1 || hosts["b.go evil2.example"] != 1 || len(hosts) != 2 {
		t.Errorf("hosts=%v", hosts)
	}
	if len(banned) != 2 {
		t.Errorf("dot import and x/sys/unix must both be banned: %v", banned)
	}
}

func TestDashboardMakesNoUnreviewedNetworkConnections(t *testing.T) {
	uses, hosts, banned := scanEgress(t, dashboardSources(t))
	if len(banned) != 0 {
		t.Fatalf("banned imports: %v", banned)
	}
	for _, key := range sortedKeys(uses) {
		allowed, ok := allowedEgress[key]
		if !ok || allowed.count != uses[key] {
			t.Errorf("%s used %d times, reviewed allowance %+v; add a Tor or stack-internal justification to allowedEgress", key, uses[key], allowed)
		}
	}
	for key, allowed := range allowedEgress {
		if allowed.reason == "" {
			t.Errorf("%s has no reason", key)
		}
		if uses[key] == 0 {
			t.Errorf("stale allowance %s", key)
		}
	}
	for _, key := range sortedKeys(hosts) {
		host := strings.SplitN(key, " ", 2)[1]
		if allowedURLHosts[host] == "" {
			t.Errorf("%s names a host outside the reviewed list", key)
		}
	}
}

func TestExternalHostsAreOnlyNamedByFilesThatUseTheTorClient(t *testing.T) {
	_, hosts, _ := scanEgress(t, dashboardSources(t))
	torFiles := map[string]string{"api.telegram.org": "monitor_io.go", "api.coingecko.com": "prices.go", "api.github.com": "monitor.go"}
	for host, file := range torFiles {
		if hosts[file+" "+host] == 0 {
			t.Errorf("%s no longer names %s; update this test with the new Tor-client owner", file, host)
		}
		for key := range hosts {
			if strings.HasSuffix(key, " "+host) && !strings.HasPrefix(key, file+" ") {
				t.Errorf("%s names %s outside %s", key, host, file)
			}
		}
	}
}

func TestExternalClientsAreBuiltOnlyWithTheTorDialer(t *testing.T) {
	monitor := NewMonitor("1.0.0", t.TempDir(), t.TempDir())
	cases := map[string]*http.Client{
		"monitor":      monitor.client,
		"fresh":        monitor.freshTorClient(),
		"price lookup": NewPriceCache().client,
	}
	for name, client := range cases {
		transport, ok := client.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("%s client transport = %T", name, client.Transport)
		}
		if transport.Proxy != nil {
			t.Errorf("%s client honours proxy environment variables", name)
		}
		if transport.DialContext == nil || transport.Dial != nil || transport.DialTLS != nil || transport.DialTLSContext != nil {
			t.Errorf("%s client does not dial only through the SOCKS dialer", name)
		}
		if client.CheckRedirect == nil {
			t.Errorf("%s client follows redirects to arbitrary hosts", name)
		}
	}
	if torProxy != "172.29.0.25:9050" {
		t.Fatalf("torProxy = %s", torProxy)
	}
}

func TestStackInternalURLsRejectEverythingElse(t *testing.T) {
	for _, ok := range []string{"http://172.29.0.26:8332", "http://127.0.0.1:8332", "http://localhost:8332", "http://tari:18142", "http://10.1.2.3:1", "http://[fd00::1]:1"} {
		if err := validateInternalHTTPURL("X", ok); err != nil {
			t.Errorf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"http://8.8.8.8:8332", "http://example.com:8332", "http://api.telegram.org", "https://172.29.0.26:8332", "http://user:pw@172.29.0.26:8332",
		"http://172.29.0.26:8332/path", "http://172.29.0.26:8332?x=1", "http://[2606:4700::1111]:1", "http://abcdefghijklmnop.onion:80", "ftp://172.29.0.26", "",
		"http://172.32.0.1:1", "http://100.64.0.1:1", "http://1.1.1.1",
	} {
		if err := validateInternalHTTPURL("X", bad); err == nil {
			t.Errorf("%s was accepted as an internal node URL", bad)
		}
	}
}
