import {test} from 'node:test';
import assert from 'node:assert/strict';
import {powerView,powerRequest,controlView,chainFeesView,nodeBecameActive} from './chains.js';
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
