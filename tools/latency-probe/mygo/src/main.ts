// Probe, SweepRow and events are generated from the Go code by `mygo generate`.
import { Probe, events, type SweepRow } from "./mygo";

const $ = <T extends HTMLElement>(sel: string) => document.querySelector<T>(sel)!;

const SLOT_MS = 7.98;
const series: number[] = [];
const MAX_POINTS = 600;

const fmt = (v: number) => (v === undefined || v === null ? "—" : `${v.toFixed(3)}`);
const fmtMs = (v: number) => (v === undefined || v === null ? "—" : `${v.toFixed(3)} ms`);

function setStat(id: string, value: number, bad: boolean) {
  const el = $(`#${id}`);
  el.textContent = fmtMs(value);
  el.closest(".stat")?.classList.toggle("bad", bad);
}

function renderRows(rows: SweepRow[], active?: number) {
  const card = $("#sweep-card");
  card.hidden = rows.length === 0;
  const tbody = card.querySelector("tbody")!;
  tbody.innerHTML = "";

  for (const r of rows) {
    const tr = document.createElement("tr");
    if (active !== undefined && r.burn === active) tr.classList.add("active");
    const bad = r.overPct > 0;
    tr.innerHTML =
      `<td>${r.burn} / core${r.burn === 1 ? "" : "s"}</td>` +
      `<td>${fmt(r.p50)}</td>` +
      `<td>${fmt(r.p95)}</td>` +
      `<td>${fmt(r.p99)}</td>` +
      `<td>${fmt(r.max)}</td>` +
      `<td class="${bad ? "bad" : ""}">${r.overPct.toFixed(1)}%</td>` +
      `<td>${r.stalls}</td>`;
    tbody.appendChild(tr);
  }
}

function draw() {
  const canvas = $<HTMLCanvasElement>("#chart");
  const ctx = canvas.getContext("2d");
  if (!ctx) return;

  const dark = matchMedia("(prefers-color-scheme: dark)").matches;
  const w = canvas.width;
  const h = canvas.height;
  ctx.clearRect(0, 0, w, h);

  const ceiling = Math.max(SLOT_MS * 1.6, ...series, SLOT_MS);
  const y = (v: number) => h - (v / ceiling) * h;

  const slotY = y(SLOT_MS);
  ctx.strokeStyle = dark ? "#4b5563" : "#cbd5e1";
  ctx.setLineDash([6, 4]);
  ctx.beginPath();
  ctx.moveTo(0, slotY);
  ctx.lineTo(w, slotY);
  ctx.stroke();
  ctx.setLineDash([]);

  ctx.fillStyle = dark ? "#9aa0a6" : "#6b7280";
  ctx.font = "12px system-ui, sans-serif";
  ctx.fillText(`${SLOT_MS} ms deadline`, 6, Math.max(14, slotY - 6));

  if (series.length < 2) {
    ctx.fillText("waiting for samples…", 6, h / 2);
    return;
  }

  const step = w / (series.length - 1);
  ctx.strokeStyle = dark ? "#60a5fa" : "#2563eb";
  ctx.lineWidth = 1.5;
  ctx.beginPath();
  series.forEach((v, i) => {
    const px = i * step;
    const py = y(v);
    if (i === 0) ctx.moveTo(px, py);
    else ctx.lineTo(px, py);
  });
  ctx.stroke();

  ctx.fillStyle = dark ? "#f87171" : "#dc2626";
  series.forEach((v, i) => {
    if (v > SLOT_MS) ctx.fillRect(i * step - 1.5, y(v), 3, Math.max(3, slotY - y(v)));
  });
}

async function showEnv() {
  const e = await Probe.environment();
  $("#env").textContent =
    `${e.cpus} logical CPUs · GOMAXPROCS ${e.gomaxprocs} · ${e.mode}`;
}

$("#run").addEventListener("click", async () => {
  const slots = Number($<HTMLInputElement>("#slots").value) || 1500;
  const burn = Number($<HTMLInputElement>("#burn").value) || 0;
  series.length = 0;
  renderRows([]);
  $("#status").textContent = "running…";
  $("#status").textContent = await Probe.run(slots, burn);
});

$("#sweep").addEventListener("click", async () => {
  const slots = Number($<HTMLInputElement>("#slots").value) || 800;
  series.length = 0;
  renderRows([]);
  $("#status").textContent = await Probe.sweep(slots);
});

$("#stop").addEventListener("click", async () => {
  await Probe.stop();
});

events.progress.on((p) => {
  if (p.latest !== undefined) {
    series.push(p.latest);
    if (series.length > MAX_POINTS) series.shift();
  }
  setStat("p50", p.p50, p.p50 > SLOT_MS);
  setStat("p99", p.p99, p.p99 > SLOT_MS);
  setStat("max", p.max, p.max > SLOT_MS);
  $("#over").textContent = `${p.overPct.toFixed(1)}%`;
  $("#status").textContent = p.running
    ? `${p.phase} · ${p.done}/${p.total} slots · ${p.elapsed.toFixed(1)} s`
    : `${p.phase} done · ${p.p50.toFixed(3)} / ${p.p99.toFixed(3)} / ${p.max.toFixed(3)} ms · ${p.overPct.toFixed(1)}% late`;
  draw();
});

events.sweep.on((s) => {
  renderRows(s.rows, s.active);
  if (s.done) {
    const worst = s.rows[s.rows.length - 1];
    $("#status").textContent = `sweep complete on ${s.cores} cores · worst level ${worst?.burn ?? 0} at ${worst?.overPct.toFixed(1) ?? "0"}% late`;
  } else {
    $("#status").textContent = `sweeping… level burn=${s.active} on ${s.cores} cores`;
  }
});

draw();
void showEnv();
