import { NODE_IDS, genericNodeView, updateTowerProgress } from "./nodes.js";
import { dashboardFetch as fetch } from "./session.js";

export const NODE_LABELS = {
  bitcoin: "Bitcoin",
  monero: "Monero",
  tari: "Tari",
  litecoin: "Litecoin",
  dogecoin: "Dogecoin",
  bitcoincash: "Bitcoin Cash",
  zcash: "Zcash",
};

export function powerView(node) {
  if (!node?.configured) return {active:false,disabled:true,button:"Not configured",status:"This node is not configured."};
  if (!node?.supported) return { active: false, disabled: true, button: "Unavailable", status: "This node is unavailable in this build." };
  if (node.active) return { active: true, disabled: false, button: "Deactivate node", status: "Deactivate this node to free its CPU and memory. Stored blockchain data is kept." };
  return { active: false, disabled: false, button: "Activate node", status: "Node is inactive" };
}

export function powerRequest(node, confirmation = "") {
  const active = node?.active === true;
  if (active && confirmation !== "deactivate") throw new Error('Type "deactivate" to continue.');
  return { active: !active, confirmation: active ? confirmation : "" };
}

export function chainFeesView(id, data) {
  const standard = id === "zcash";
  const targeted = id !== "bitcoincash" && !standard;
  const values = data.fees_available && Array.isArray(data.fee_estimates) ? data.fee_estimates : [];
  return {
    label: standard ? "Standard fee (zats/action):" : data.fee_unit ? `Fee ${data.fee_unit}${targeted ? " (1/3/6 blocks)" : ""}:` : "Fee estimates:",
    value: values.length ? (standard ? String(values[0]) : values.join(" / ")) : "Unavailable",
  };
}

function byId(id) { return document.getElementById(id); }

function setText(id, value) {
  const element = byId(id);
  if (element) element.textContent = value;
}

function announceNodeStatus(id) {
  window.dispatchEvent(new CustomEvent("node-status-updated", { detail: { node: id } }));
}

function setNodeActive(id, node) {
  const view = powerView(node);
  for (const choice of document.querySelectorAll(`[data-select-node="${id}"]`)) {
    choice.hidden = node.configured !== true;
    choice.dataset.nodeActive = view.active ? "1" : "0";
    choice.classList.toggle("inactive", !view.active);
    choice.setAttribute("aria-label", `${NODE_LABELS[id]}${view.active ? "" : " — inactive"}`);
  }
  const panel = byId(`${id}-panel`);
  if (panel) panel.dataset.nodeActive = view.active ? "1" : "0";
  const button = document.querySelector(`[data-node-power="${id}"]`);
  if (button) {
    button.textContent = view.button;
    button.disabled = view.disabled;
    button.dataset.active = view.active ? "true" : "false";
  }
  setText(`${id}-power-status`, view.status);
  if (!view.active) {
    setText(`${id}-badge`, view.disabled ? "Unavailable" : "Inactive");
    setText(`${id}-state`, view.status);
    const details = byId(`${id}-details`);
    if (details) details.hidden = true;
    const live = byId(`${id}-live`);
    if (live) live.dataset.nextBlock = "";
  }
  announceNodeStatus(id);
  return view;
}

function renderNode(id, data) {
  const view = genericNodeView(data, NODE_LABELS[id]);
  setText(`${id}-badge`, view.badge);
  setText(`${id}-state`, view.state);
  const details = byId(`${id}-details`);
  if (details) details.hidden = !view.details;
  const live = byId(`${id}-live`);
  if (live) live.dataset.nextBlock = view.nextBlock || "";
  updateTowerProgress(live, data);
  if (!view.details) {
    announceNodeStatus(id);
    return;
  }
  setText(`${id}-version`, view.version);
  setText(`${id}-uptime`, view.uptime);
  setText(`${id}-connections`, view.connections);
  setText(`${id}-blocks`, view.blocks);
  const progress = byId(`${id}-progress`);
  if (progress) progress.style.width = `${view.progress}%`;
  setText(`${id}-sync`, `${view.progress.toFixed(4)}%`);
  setText(`${id}-size`, view.size);
  setText(`${id}-disk-capacity`, view.diskCapacity);
  setText(`${id}-disk-usage`, view.diskUsage);
  const diskFree = byId(`${id}-disk-free`);
  if (diskFree) {
    diskFree.textContent = `${view.diskFree}${view.diskLow ? " ⚠ LOW" : ""}`;
    diskFree.classList.toggle("warn", view.diskLow);
  }
  setText(`${id}-mempool`, view.mempool);
  setText(`${id}-last-block`, view.lastBlock);
  setText(`${id}-rpc-exposure`, view.rpcExposure);
  setText(`${id}-updated`, `Updated: ${view.updated}`);
  const feeSection=byId(`${id}-fee-section`);
  if(feeSection) feeSection.hidden=data.fees_available!==true;
  const fees=chainFeesView(id,data);
  setText(`${id}-fees`, fees.value);
  setText(`${id}-fee-unit`, fees.label);
  announceNodeStatus(id);
}

export function controlView(payload) {
  const status=payload?.status;
  const valid=status && /^[0-9a-f]{32}$/.test(status.id || "") && NODE_IDS.includes(status.node) && ["pending","applied","rejected","failed"].includes(status.state);
  return {available:payload?.control_available===true,pending:payload?.control_pending===true || valid && status.state==="pending",status:valid?status:null};
}

export function nodeBecameActive(previous, current) {
  return current?.active === true && previous?.active !== true;
}

if (typeof document !== "undefined") {
  const state=new Map();
  const dialog=byId("node-power-dialog"), form=byId("node-power-form"), input=byId("node-power-confirm");
  const confirmLabel=byId("node-power-confirm-label"), submit=byId("node-power-submit");
  let selected="", control={available:false,pending:false,status:null}, poll, busy=false, requestID="";
  try {requestID=localStorage.getItem("nodeStackPowerRequest") || "";} catch {}

  const refreshNode=async(id)=>{
    const node=state.get(id);
    if(!node?.configured || !node.active || !node.supported || id==="bitcoin" || id==="monero") return;
    try {
      const response=await fetch(`/api/${id}`,{headers:{"X-Requested-With":"fetch"}});
      const data=await response.json();
      if(!response.ok && !data?.enabled) throw new Error("unavailable");
      renderNode(id,data);
    } catch {renderNode(id,{enabled:true,available:false});}
  };

  const updateButtons=()=>{
    for(const id of NODE_IDS) {
      const node=state.get(id), button=document.querySelector(`[data-node-power="${id}"]`);
      if(!button || !node) continue;
      const view=powerView(node), pending=control.pending && control.status?.node===id;
      button.disabled=view.disabled || !control.available || control.pending || busy;
      button.textContent=pending ? (control.status.active ? "Activating…" : "Deactivating…") : view.button;
      if(pending) setText(`${id}-power-status`, "Applying node state change…");
      else if(node.configured && !control.available) setText(`${id}-power-status`, "Node controls require the installed host helper.");
    }
  };

  const loadNodes=async()=>{
    clearTimeout(poll);
    try {
      const response=await fetch("/api/nodes",{headers:{"X-Requested-With":"fetch"}});
      if(!response.ok) throw new Error("unavailable");
      const payload=await response.json();
      control=controlView(payload);
      for(const id of NODE_IDS) {
        const node=payload.nodes?.find(item=>item.id===id);
        if(!node) continue;
        const previous=state.get(id);
        state.set(id,node);
        setNodeActive(id,node);
        if(nodeBecameActive(previous,node)) window.dispatchEvent(new CustomEvent("node-power-updated",{detail:{id,active:true}}));
        refreshNode(id);
      }
      const configured=[...state.values()].some(node=>node.configured);
      setText("node-overview-status",configured ? "" : "No nodes are configured. Install a node in Settings.");
      if(requestID && control.status?.id===requestID && ["applied","rejected","failed"].includes(control.status.state)) {
        const result=control.status;
        setText(`${result.node}-power-status`,result.message || (result.state==="applied" ? "Node state updated." : "Node state could not be changed."));
        requestID="";
        try {localStorage.removeItem("nodeStackPowerRequest");} catch {}
      }
      updateButtons();
      updateSubmit();
    } catch {
      setText("node-overview-status","Could not refresh node status. Retrying…");
      control.available=false;
      updateButtons();
      updateSubmit();
    }
    poll=setTimeout(loadNodes,control.pending || requestID ? 2000 : 30000);
  };

  const updateSubmit=()=>{
    const node=state.get(selected);
    if(submit) submit.disabled=busy || !control.available || control.pending || !node || node.active===true && input?.value!=="deactivate";
  };

  document.addEventListener("click",event=>{
    const button=event.target.closest?.("[data-node-power]");
    if(!button || button.disabled) return;
    selected=button.dataset.nodePower;
    const node=state.get(selected);
    if(!node) return;
    const action=node.active ? "Deactivate" : "Activate";
    setText("node-power-title",`${action} ${NODE_LABELS[selected]}`);
    setText("node-power-description",node.active ? "The node will stop. Its stored data will be kept, and you can activate it again." : "The node will start using its existing data and settings.");
    setText("node-power-error","");
    submit.textContent=`${action} node`;
    confirmLabel.hidden=!node.active;
    input.value="";
    updateSubmit();
    dialog.showModal();
  });
  input?.addEventListener("input",updateSubmit);
  byId("node-power-cancel")?.addEventListener("click",()=>dialog.close());
  form?.addEventListener("submit",async event=>{
    event.preventDefault();
    const node=state.get(selected);
    if(!node || busy || !control.available || control.pending) return;
    let body;
    try {body=powerRequest(node,input.value);} catch(error) {setText("node-power-error",error.message);return;}
    busy=true; updateSubmit(); updateButtons();
    try {
      const response=await fetch(`/api/nodes/${selected}/power`,{method:"POST",headers:{"Content-Type":"application/json","X-Requested-With":"fetch"},body:JSON.stringify(body)});
      if(!response.ok) throw new Error((await response.text()).trim().slice(0,200) || "The node state could not be changed.");
      const result=await response.json();
      if(!/^[0-9a-f]{32}$/.test(result?.id || "")) throw new Error("The node state request could not be confirmed.");
      requestID=result.id;
      try {localStorage.setItem("nodeStackPowerRequest",requestID);} catch {}
      control={...control,pending:true,status:{...result,node:selected,active:body.active}};
      dialog.close();
      setText(`${selected}-power-status`,"Applying node state change…");
    } catch(error) {setText("node-power-error",error.message || "The node state could not be confirmed. Refresh the status before trying again.");}
    finally {busy=false;updateSubmit();updateButtons();await loadNodes();}
  });
  window.addEventListener("node-catalog-updated", loadNodes);
  loadNodes();
}
