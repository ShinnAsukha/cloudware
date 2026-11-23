"use strict";

const tokenElement = document.querySelector('meta[name="cw-token"]');
const token = tokenElement ? tokenElement.content : "";
const statusElement = document.getElementById("message");
const logsElement = document.getElementById("logs");
const verifiedElement = document.getElementById("verified");
const approvalElement = document.getElementById("approve");
const installButton = document.getElementById("install");
const checkButton = document.getElementById("check");
const downloadButton = document.getElementById("download");

async function readState() {
  const response = await fetch("/api/state", {
    cache: "no-store",
    headers: { "X-Cloudware-Token": token }
  });
  if (!response.ok) throw new Error("Yerel durum alınamadı: HTTP " + response.status);
  return response.json();
}

function paint(state) {
  statusElement.textContent = state.message || "Hazır";
  logsElement.textContent = state.logs || "Henüz işlem kaydı yok.";
  const tag = state.verified_tag || "";
  verifiedElement.textContent = tag
    ? "İndirilen ve doğrulanan hedef sürüm: " + tag
    : "Henüz doğrulanmış bir sürüm yok.";
  checkButton.disabled = Boolean(state.busy);
  downloadButton.disabled = Boolean(state.busy);
  installButton.disabled = Boolean(state.busy) || !tag ||
    approvalElement.value.trim() !== "KUR " + tag;
}

async function refresh() {
  try {
    const state = await readState();
    paint(state);
  } catch (error) {
    statusElement.textContent = "Bağlantı hatası: " + error.message;
    installButton.disabled = true;
  }
}

async function takeAction(action) {
  const approval = action === "install" ? approvalElement.value.trim() : "";
  if (action === "install") {
    const answer = window.confirm("Doğrulanmış Cloudware güncellemesi kurulacak. Mevcut kaynaklar yedeklenecek. Devam edilsin mi?");
    if (!answer) return;
  }
  try {
    const response = await fetch("/api/action", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Cloudware-Token": token
      },
      body: JSON.stringify({ action: action, approval: approval })
    });
    const result = await response.json();
    if (!response.ok) {
      statusElement.textContent = "İşlem reddedildi: " + (result.message || response.status);
    } else {
      statusElement.textContent = result.message || "İşlem başladı";
    }
  } catch (error) {
    statusElement.textContent = "İstek başarısız: " + error.message;
  }
  await refresh();
}

checkButton.addEventListener("click", function () { void takeAction("check"); });
downloadButton.addEventListener("click", function () { void takeAction("download"); });
installButton.addEventListener("click", function () { void takeAction("install"); });
approvalElement.addEventListener("input", function () { void refresh(); });
void refresh();
setInterval(function () { void refresh(); }, 1250);
