// Thin wrapper around the backend HTTP API. All calls are same-origin: in dev
// Vite proxies /api to the Go backend; in prod they share an origin.

export async function submitBuild(opts) {
  const res = await fetch('/api/build', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(opts),
  })
  if (!res.ok) throw new Error(`submit failed: ${res.status}`)
  return res.json()
}

export async function fetchInventory() {
  const res = await fetch('/api/inventory')
  if (!res.ok) throw new Error(`inventory failed: ${res.status}`)
  return res.json()
}

export async function fetchCatalog() {
  const res = await fetch('/api/catalog')
  if (!res.ok) throw new Error(`catalog failed: ${res.status}`)
  return res.json()
}

// sel: { os, system_version, include_nvidia, nvidia_version, cuda_version,
//        include_ofed, ofed_version }
export async function fetchDefaultName(sel) {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(sel)) {
    if (v !== undefined && v !== null && v !== '') params.set(k, String(v))
  }
  const res = await fetch(`/api/default-name?${params}`)
  if (!res.ok) throw new Error(`default-name failed: ${res.status}`)
  return res.json()
}

export async function preflight() {
  const res = await fetch('/api/preflight')
  if (!res.ok) throw new Error(`preflight failed: ${res.status}`)
  return res.json()
}

export async function listJobs() {
  const res = await fetch('/api/jobs')
  if (!res.ok) throw new Error(`list failed: ${res.status}`)
  return res.json()
}

export async function fetchLog(id) {
  const res = await fetch(`/api/jobs/${id}/log`)
  if (!res.ok) throw new Error(`log failed: ${res.status}`)
  return res.text()
}

export function downloadURL(id) {
  return `/api/jobs/${id}/download`
}

export async function fetchToolpkgs() {
  const res = await fetch('/api/toolpkgs')
  if (!res.ok) throw new Error(`toolpkgs failed: ${res.status}`)
  return res.json()
}

export async function downloadToolpkg(group) {
  const res = await fetch(`/api/toolpkgs/${group}/download`, { method: 'POST' })
  if (!res.ok) throw new Error((await res.text()) || `download failed: ${res.status}`)
  return res.json()
}

export async function fetchToolpkgLog(group) {
  const res = await fetch(`/api/toolpkgs/${group}/log`)
  if (!res.ok) throw new Error(`log failed: ${res.status}`)
  return res.text()
}

export async function fetchNvaptVersions() {
  const res = await fetch('/api/nvapt/versions')
  if (!res.ok) throw new Error(`nvapt versions failed: ${res.status}`)
  return res.json()
}

export async function fetchNvaptStatus() {
  const res = await fetch('/api/nvapt/status')
  if (!res.ok) throw new Error(`nvapt status failed: ${res.status}`)
  return res.json()
}

export async function downloadNvapt(driver, toolkit) {
  const res = await fetch('/api/nvapt/download', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ driver, toolkit }),
  })
  if (!res.ok) throw new Error((await res.text()) || `download failed: ${res.status}`)
  return res.json()
}

export async function fetchNvaptLog() {
  const res = await fetch('/api/nvapt/log')
  if (!res.ok) throw new Error(`log failed: ${res.status}`)
  return res.text()
}

export async function fetchDocaInfo() {
  const res = await fetch('/api/doca/info')
  if (!res.ok) throw new Error(`doca info failed: ${res.status}`)
  return res.json()
}
