package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"time"
)

type Docker struct{ client *http.Client }

type Container struct {
	ID     string            `json:"Id"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

type DockerInfo struct {
	NCPU          int    `json:"NCPU"`
	MemTotal      uint64 `json:"MemTotal"`
	DockerRootDir string `json:"DockerRootDir"`
}

func NewDocker(socket string) *Docker {
	return &Docker{client: &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socket)
			},
			DisableKeepAlives: true,
		},
	}}
}

func (d *Docker) get(path string, out any) error {
	resp, err := d.client.Get("http://docker" + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker %s: status %d", path, resp.StatusCode)
	}
	return json.Unmarshal(body, out)
}

func (d *Docker) Info() (DockerInfo, error) {
	var info DockerInfo
	err := d.get("/info", &info)
	return info, err
}

func (d *Docker) Containers(project string) ([]Container, error) {
	filters, _ := json.Marshal(map[string][]string{"label": {"com.docker.compose.project=" + project}})
	var list []Container
	err := d.get("/containers/json?all=1&filters="+url.QueryEscape(string(filters)), &list)
	return list, err
}

func (d *Docker) VolumeMountpoint(name string) (string, error) {
	var v struct{ Name, Mountpoint string }
	if err := d.get("/volumes/"+url.PathEscape(name), &v); err != nil {
		return "", err
	}
	if v.Name != name || !filepath.IsAbs(v.Mountpoint) {
		return "", fmt.Errorf("docker volume %s: unusable answer", name)
	}
	return v.Mountpoint, nil
}
