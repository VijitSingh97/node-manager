export function askConfirm(message, confirmLabel = "Confirm") {
  const dialog = document.getElementById("confirm-dialog");
  dialog.querySelector("#confirm-dialog-message").textContent = message;
  dialog.querySelector("#confirm-dialog-ok").textContent = confirmLabel;
  dialog.returnValue = "";
  return new Promise((resolve) => {
    dialog.addEventListener("close", () => resolve(dialog.returnValue === "ok"), { once: true });
    dialog.showModal();
  });
}
