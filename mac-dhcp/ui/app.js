const $ = (id) => document.getElementById(id);
const setText = (id, text) => {
  const el = $(id);
  if (el) el.textContent = text;
};

function toast(msg, type = "ok") {
  const el = $("toast");
  el.textContent = msg;
  el.className = "toast " + type;
  clearTimeout(toast._t);
  toast._t = setTimeout(() => {
    el.className = "toast hidden";
  }, 3200);
}

async function api(path, opts) {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...opts,
  });
  const data = await res.json();
  if (!res.ok && !data.ok) throw new Error(data.error || res.statusText);
  return data;
}

function setBusy(busy) {
  ["btnStart", "btnStop", "btnSetIp", "btnInstall", "btnDetect", "btnRefresh", "btnLogs"].forEach((id) => {
    const b = $(id);
    if (b) b.disabled = busy;
  });
}

function formEnv() {
  const fd = new FormData($("cfgForm"));
  const env = {};
  fd.forEach((v, k) => {
    env[k] = String(v).trim();
  });
  return env;
}

function stripAnsi(s) {
  return String(s || "").replace(/\x1b\[[0-9;]*m/g, "");
}

function linkLabel(status) {
  if (status === "active") return "已接通";
  if (status === "inactive") return "未接通";
  if (status === "missing") return "网卡不存在";
  return status || "-";
}

function renderStatus(s) {
  setText("clock", s.time || "");
  const badge = $("runBadge");
  if (badge) {
    if (s.running) {
      badge.textContent = "DHCP 运行中";
      badge.className = "badge on";
    } else {
      badge.textContent = "DHCP 已停止";
      badge.className = "badge off";
    }
  }
  setText("sIface", s.interface || "-");
  const link = s.iface_status || "-";
  setText("sLink", linkLabel(link));
  const linkEl = $("sLink");
  if (linkEl) {
    if (link === "active") linkEl.style.color = "var(--ok)";
    else if (link === "missing") linkEl.style.color = "var(--danger)";
    else linkEl.style.color = "var(--warn)";
  }

  setText("sIp", s.iface_ip || "-");
  setText("sServerIp", s.server_ip || "-");
  const ipEl = $("sIp");
  if (ipEl) ipEl.style.color = s.ip_ok ? "var(--ok)" : "var(--warn)";
  setText("sRange", s.range || "-");
  setText("sLeases", `${s.active_leases || 0} / ${s.lease_count || 0}`);
  setText("sInstalled", s.installed ? "已安装" : "未安装");
  const instEl = $("sInstalled");
  if (instEl) instEl.style.color = s.installed ? "var(--ok)" : "var(--warn)";

  const box = $("warnBox");
  const warns = s.warnings || [];
  if (box) {
    if (warns.length) {
      box.className = "warn-box";
      box.innerHTML =
        "<strong>注意</strong><ul>" +
        warns.map((w) => `<li>${esc(w)}</li>`).join("") +
        "</ul>";
    } else {
      box.className = "warn-box hidden";
      box.innerHTML = "";
    }
  }
}

function renderLeases(leases) {
  const body = $("leaseBody");
  if (!leases || !leases.length) {
    body.innerHTML = '<tr><td colspan="5" class="empty">暂无租约</td></tr>';
    return;
  }
  body.innerHTML = leases
    .map(
      (r) => `<tr>
      <td>${esc(r.ip)}</td>
      <td>${esc(r.mac)}</td>
      <td>${esc(r.hostname || "-")}</td>
      <td>${esc(r.expiry_text)}</td>
      <td><span class="pill ${r.active ? "ok" : "dead"}">${r.active ? "有效" : "过期"}</span></td>
    </tr>`
    )
    .join("");
}

function linkPill(status) {
  if (status === "active") return '<span class="pill ok">已接通</span>';
  if (status === "inactive") return '<span class="pill warn">未接通</span>';
  return `<span class="pill dead">${esc(status || "?")}</span>`;
}

function isWifi(row) {
  const t = `${row.service || ""} ${row.hardware || ""}`;
  return /wi-?fi|airport/i.test(t);
}

function renderIfaces(ifaces) {
  const body = $("ifaceBody");
  if (!ifaces || !ifaces.length) {
    body.innerHTML = '<tr><td colspan="6" class="empty">未识别到可用网卡</td></tr>';
    return;
  }
  body.innerHTML = ifaces
    .map((r) => {
      const wifi = isWifi(r);
      const cls = [
        "iface-row",
        r.selected ? "selected" : "",
        wifi ? "wifi" : "",
      ]
        .filter(Boolean)
        .join(" ");
      const tag = r.selected
        ? '<span class="pill sel">当前</span>'
        : wifi
          ? '<span class="pill dead">Wi-Fi</span>'
          : '<button type="button" class="btn ghost linkish btn-pick">选用</button>';
      const hw = r.hardware || "-";
      return `<tr class="${cls}" data-iface="${esc(r.iface)}" data-service="${esc(
        r.service === "-" ? "" : r.service
      )}" data-wifi="${wifi ? "1" : "0"}">
        <td><strong>${esc(r.iface)}</strong></td>
        <td>${esc(r.service)}</td>
        <td class="hw" title="${esc(hw)}">${esc(hw)}</td>
        <td>${esc(r.ip)}</td>
        <td>${linkPill(r.status)}</td>
        <td>${tag}</td>
      </tr>`;
    })
    .join("");

  body.querySelectorAll("tr.iface-row").forEach((tr) => {
    tr.addEventListener("click", () => pickIface(tr));
  });
}

function pickIface(tr) {
  const iface = tr.getAttribute("data-iface");
  const service = tr.getAttribute("data-service") || "";
  const wifi = tr.getAttribute("data-wifi") === "1";
  if (!iface) return;
  if (wifi && !confirm(`「${iface}」是 Wi-Fi，通常不建议开 DHCP（易与现网冲突）。仍要选用？`)) {
    return;
  }
  const form = $("cfgForm");
  form.INTERFACE.value = iface;
  form.NETWORK_SERVICE.value = service;
  toast(`已填入 INTERFACE=${iface}` + (service ? `，服务名=${service}` : ""));
  // 高亮
  document.querySelectorAll("#ifaceBody tr").forEach((r) => r.classList.remove("selected"));
  tr.classList.add("selected");
}

function fillForm(env) {
  const form = $("cfgForm");
  [...form.elements].forEach((el) => {
    if (!el.name) return;
    if (env[el.name] != null) el.value = env[el.name];
  });
}

function esc(s) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

async function refresh() {
  try {
    const [st, ls] = await Promise.all([api("/api/status"), api("/api/leases")]);
    renderStatus(st);
    renderLeases(ls.leases || []);
    renderIfaces(st.ifaces || []);
  } catch (e) {
    toast("刷新失败: " + e.message, "err");
  }
}

async function loadConfig() {
  const data = await api("/api/config");
  fillForm(data.env || {});
}

async function doAction(action, label) {
  // 开启前若有风险提示，先确认
  if (action === "start") {
    try {
      const st = await api("/api/status");
      const warns = st.warnings || [];
      if (warns.length) {
        const msg =
          "当前存在以下问题，仍要开启吗？\n\n- " +
          warns.join("\n- ");
        if (!confirm(msg)) return;
      }
    } catch (_) {}
  }
  if (action === "setip") {
    const env = formEnv();
    if (!env.INTERFACE || !env.SERVER_IP || !env.NETMASK) {
      toast("请先填写 INTERFACE / SERVER_IP / NETMASK", "err");
      return;
    }
    if (
      !confirm(
        `将为网卡 ${env.INTERFACE} 配置：\nIP ${env.SERVER_IP}\n掩码 ${env.NETMASK}\n网关 ${env.ROUTER || env.SERVER_IP}\n\n继续？`
      )
    ) {
      return;
    }
  }
  setBusy(true);
  $("output").textContent = `正在执行：${label} …\n（若弹出密码框请输入本机管理员密码）`;
  try {
    const payload = { action };
    if (action === "setip" || action === "install" || action === "start") {
      payload.env = formEnv();
    }
    const data = await api("/api/action", {
      method: "POST",
      body: JSON.stringify(payload),
    });
    $("output").textContent = stripAnsi(data.output || "(无输出)");
    if (data.status) {
      renderStatus(data.status);
      if (data.status.ifaces) renderIfaces(data.status.ifaces);
    }
    await refresh();
    toast(data.ok ? `${label} 完成` : `${label} 失败`, data.ok ? "ok" : "err");
  } catch (e) {
    $("output").textContent = String(e);
    toast(label + " 失败", "err");
  } finally {
    setBusy(false);
  }
}

$("btnRefresh").onclick = refresh;
$("btnStart").onclick = () => doAction("start", "开启 DHCP");
$("btnStop").onclick = () => doAction("stop", "关闭 DHCP");
$("btnSetIp").onclick = () => doAction("setip", "配置网卡 IP");
$("btnInstall").onclick = () => doAction("install", "安装 / 应用配置");
$("btnDetect").onclick = () => doAction("detect", "探测网卡");

$("btnLogs").onclick = async () => {
  try {
    const data = await api("/api/logs");
    $("output").textContent =
      "===== dnsmasq.log =====\n" +
      (data.dhcp || "(空)") +
      "\n\n===== dnsmasq.err.log =====\n" +
      (data.err || "(空)");
  } catch (e) {
    toast("拉取日志失败", "err");
  }
};

$("cfgForm").onsubmit = async (ev) => {
  ev.preventDefault();
  const env = formEnv();
  try {
    await api("/api/config", { method: "POST", body: JSON.stringify({ env }) });
    toast("配置已保存，请再点「安装 / 应用配置」或「配置网卡 IP」");
    await refresh();
  } catch (e) {
    toast("保存失败: " + e.message, "err");
  }
};

refresh();
loadConfig().catch(() => {});
setInterval(refresh, 5000);
