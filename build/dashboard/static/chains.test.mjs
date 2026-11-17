import {test} from 'node:test';
import assert from 'node:assert/strict';
import {powerView,powerBadge,powerRequest,controlView,chainFeesView,nodeBecameActive,setNodeActive,nodePollDelay,nodeRefreshable} from './chains.js';
import {genericNodeView,normalizeNode} from './nodes.js';
import {overviewLayout,previewDepth,GRID} from './tower.js';

const active={configured:true,supported:true,active:true};
test('Zcash shows a standard per-action fee without inventing confirmation targets',()=>{
  assert.deepEqual(chainFeesView('zcash',{fees_available:true,fee_estimates:['400'],fee_unit:'zats/action'}),{label:'Standard fee (zats/action):',value:'400'});
  assert.deepEqual(chainFeesView('litecoin',{fees_available:true,fee_estimates:['1','2','3'],fee_unit:'sat/vB'}),{label:'Fee sat/vB (1/3/6 blocks):',value:'1 / 2 / 3'});
  assert.equal(chainFeesView('zcash',{fees_available:false}).value,'Unavailable');
});
test('deactivation requires the exact confirmation and inactive nodes can reactivate',()=>{
  for(const word of ['', 'Deactivate', 'deactivate ', 'yes']) assert.throws(()=>powerRequest(active,word));
  assert.deepEqual(powerRequest(active,'deactivate'),{active:false,confirmation:'deactivate'});
  assert.deepEqual(powerRequest({...active,active:false}),{active:true,confirmation:''});
  assert.equal(powerView(active).button,'Deactivate node');
  assert.equal(powerView({...active,active:false}).button,'Activate node');
  assert.equal(powerView({...active,supported:false}).disabled,true);
  assert.equal(powerView({...active,configured:false}).disabled,true);
});
test('an active node whose container is stopped greys like inactive but offers Start without confirmation',()=>{
  const stopped={...active,stopped:true};
  const view=powerView(stopped);
  assert.deepEqual([view.active,view.stopped,view.button,view.disabled],[false,true,'Start node',false]);
  assert.match(view.status,/not running/);
  assert.deepEqual(powerRequest(stopped),{active:true,confirmation:''});
  assert.equal(powerView({...active,active:false}).stopped,undefined);
  assert.equal(powerView({...active,available:false}).button,'Deactivate node');
  assert.equal(nodeBecameActive(stopped,active),true);
  assert.equal(nodeBecameActive(undefined,stopped),false);
});
test('a node stopped after failing to start shows why and still offers Start',()=>{
  const view=powerView({...active,stopped:true,failure:'This Zakura binary requires CPU features this machine lacks: adx.'});
  assert.deepEqual([view.stopped,view.button,view.disabled],[true,'Start node',false]);
  assert.match(view.status,/failed to start\. This Zakura binary requires CPU features this machine lacks: adx\./);
});
test('a queued state change remains pending until the host confirms its outcome',()=>{
  const status={id:'a'.repeat(32),node:'litecoin',active:false,state:'pending'};
  assert.equal(controlView({control_available:true,status}).pending,true);
  assert.equal(controlView({control_available:true,status:{...status,state:'applied'}}).pending,false);
  assert.equal(controlView({control_pending:true,status:{...status,id:'bad'}}).pending,true);
  assert.equal(controlView({status:{...status,state:'invented'}}).status,null);
});
test('the first active catalog result refreshes node status',()=>{
  assert.equal(nodeBecameActive(undefined,{active:true}),true);
  assert.equal(nodeBecameActive({active:false},{active:true}),true);
  assert.equal(nodeBecameActive({active:true},{active:true}),false);
  assert.equal(nodeBecameActive(undefined,{active:false}),false);
});
test('new chain panels distinguish unavailable data from real zero values',()=>{
  for(const id of ['tari','litecoin','dogecoin','bitcoincash','zcash']) assert.equal(normalizeNode(id),id);
  const view=genericNodeView({enabled:true,available:true,height:100,target_height:200,sync_percent:50,connections_available:false,mempool_txs_available:false},'Tari');
  assert.equal(view.blocks,'100 / 200');
  assert.equal(view.progress,50);
  assert.equal(view.connections,'—');
  assert.equal(view.badge,'Full');
  assert.equal(view.mempool,'—');
  const peers=(extra)=>genericNodeView({enabled:true,available:true,connections_available:true,...extra},'Tari').connections;
  assert.equal(peers({connections:5}),'5 total');
  assert.equal(peers({connections:0,connections_split_available:true}),'0 in · 0 out');
  assert.equal(peers({connections:3,connections_split_available:true,incoming_connections:1,outgoing_connections:2}),'1 in · 2 out');
  assert.equal(genericNodeView({enabled:true,available:true,pruned:true},'Tari').badge,'Pruned');
  assert.equal(genericNodeView({enabled:true,available:true,mempool_txs:0,mempool_txs_available:true},'Litecoin').mempool,'0 tx');
});
test('the bouquet fits its canvas and staggers node heights instead of lining them up',()=>{
  for(const width of [320,768,1440,2400]) for(const count of [3,4,5,6,7]) {
    const layout=overviewLayout(width,count,900);
    assert.equal(layout.positions.length,count);
    assert.ok(new Set(layout.anchors).size>1);
    const half=GRID*layout.tw*1.12/2;
    assert.ok(layout.positions.every(x=>x-half>=0 && x+half<=layout.contentWidth));
    for(const y of layout.anchors) assert.ok(previewDepth(900,y,layout.tw)*layout.tw*0.5+y>=900);
  }
});

test('Bitcoin Cash fee does not promise confirmation targets', () => {
  assert.deepEqual(chainFeesView('bitcoincash', {fees_available:true,fee_estimates:['1'],fee_unit:'sat/B'}), {label:'Fee sat/B:',value:'1'});
});

test('a restarting node greys out, shows Restarting, and cannot be toggled',()=>{
  const view=powerView({...active,restarting:true});
  assert.deepEqual([view.active,view.restarting,view.disabled,view.button,view.status],[false,true,true,'Restarting…','Restarting after a settings change…']);
  assert.equal(powerBadge(view),'Restarting');
  assert.equal(powerView({...active,restarting:true,stopped:true}).restarting,true);
  assert.equal(powerView({...active,active:false,restarting:true}).restarting,undefined);
  assert.equal(powerView({...active,restarting:false}).active,true);
  assert.equal(powerBadge(powerView({...active,stopped:true})),'Stopped');
  assert.equal(powerBadge(powerView({...active,active:false})),'Inactive');
  assert.equal(powerBadge(powerView({...active,supported:false})),'Unavailable');
  assert.equal(nodeBecameActive({...active,restarting:true},active),false);
});
test('the tower choice and panel go grey with a Restarting badge and status line',()=>{
  const el=(extra={})=>({dataset:{},hidden:false,attrs:{},classes:new Set(),textContent:'',style:{},
    classList:{toggle(name,on){on?this.owner.classes.add(name):this.owner.classes.delete(name)}},
    setAttribute(name,value){this.attrs[name]=value},...extra});
  const make=(extra)=>{const e=el(extra);e.classList.owner=e;return e};
  const choice=make(),panel=make({querySelector:()=>null}),button=make(),texts={'litecoin-badge':make(),'litecoin-power-status':make(),'litecoin-state':make(),'litecoin-details':make(),'litecoin-live':make()};
  globalThis.document={
    querySelectorAll:()=>[choice],
    getElementById:(id)=>id==='litecoin-panel'?panel:texts[id]||null,
    querySelector:()=>button,
  };
  globalThis.window={dispatchEvent(){}};
  globalThis.CustomEvent=class{constructor(type,init){this.type=type;this.detail=init?.detail}};
  try {
    setNodeActive('litecoin',{...active,restarting:true});
    assert.equal(choice.dataset.nodeActive,'0');
    assert.equal(panel.dataset.nodeActive,'0');
    assert.equal(choice.classes.has('inactive'),true);
    assert.match(choice.attrs['aria-label'],/restarting/);
    assert.equal(texts['litecoin-badge'].textContent,'Restarting');
    assert.equal(texts['litecoin-state'].textContent,'Restarting after a settings change…');
    assert.equal(texts['litecoin-power-status'].textContent,'Restarting after a settings change…');
    assert.equal(button.disabled,true);
    setNodeActive('litecoin',active);
    assert.equal(choice.dataset.nodeActive,'1');
    assert.equal(choice.classes.has('inactive'),false);
  } finally {
    delete globalThis.document; delete globalThis.window; delete globalThis.CustomEvent;
  }
});

test('the node list polls fast while a change applies or any node restarts',()=>{
  assert.equal(nodePollDelay(true,[]),2000);
  assert.equal(nodePollDelay(true,[{restarting:true}]),2000);
  assert.equal(nodePollDelay(false,[{},{restarting:true}]),5000);
  assert.equal(nodePollDelay(false,[{},{restarting:false}]),30000);
});

test('restarting nodes are not probed by the page',()=>{
  assert.equal(nodeRefreshable('litecoin',active),true);
  assert.equal(nodeRefreshable('litecoin',{...active,restarting:true}),false);
  assert.equal(nodeRefreshable('litecoin',{...active,stopped:true}),false);
  assert.equal(nodeRefreshable('litecoin',{...active,active:false}),false);
  assert.equal(nodeRefreshable('bitcoin',active),false);
  assert.equal(nodeRefreshable('monero',active),false);
  assert.equal(nodeRefreshable('litecoin',undefined),false);
});
