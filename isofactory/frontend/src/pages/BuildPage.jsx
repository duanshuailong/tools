import { useEffect, useState, useMemo } from 'react'
import { submitBuild, fetchCatalog, fetchDefaultName, fetchNvaptVersions, fetchToolpkgs, fetchDocaInfo } from '../api.js'
import { Card, CardHeader, CardBody, Field, Select, Input, Checkbox, Alert } from '../components/ui.jsx'

// Strip the Debian revision suffix for display: 590.48.01-0ubuntu1 -> 590.48.01
const cleanDriver = v => (v || '').split('-')[0]

export default function BuildPage({ onSubmitted }) {
  const [catalog, setCatalog] = useState({ base_images: [], cuda: [], ofed: [] })
  const [nv, setNv] = useState({ available: true, drivers: [], toolkits: [] })
  const [nvLoading, setNvLoading] = useState(true)
  const [os, setOs] = useState('ubuntu')
  const [sysVer, setSysVer] = useState('')
  const [includeNvidia, setIncludeNvidia] = useState(true)
  const [nvDriver, setNvDriver] = useState('')   // apt cuda-drivers version
  const [nvToolkit, setNvToolkit] = useState('') // cuda-toolkit-XX-Y package
  const [includeOfed, setIncludeOfed] = useState(true)
  const [ofedSource, setOfedSource] = useState('doca')   // "doca" (new) | "mlnx" (legacy)
  const [ofedVer, setOfedVer] = useState('')
  const [doca, setDoca] = useState(null)                 // {available, version, ofed_version}
  const [toolGroups, setToolGroups] = useState([])       // available groups
  const [toolStatus, setToolStatus] = useState([])       // per-group deb counts
  const [selectedTools, setSelectedTools] = useState([]) // chosen group names
  const [name, setName] = useState('')
  const [defaultName, setDefaultName] = useState('')
  const [missing, setMissing] = useState([])
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  // Base images + OFED from catalog.
  useEffect(() => {
    fetchCatalog().then(c => {
      setCatalog(c)
      if (c.base_images?.length) setSysVer(c.base_images[0].version)
      if (c.ofed?.length) setOfedVer(c.ofed[0].version)
    }).catch(() => {})
  }, [])

  // Independent driver + CUDA versions from the NVIDIA apt repo.
  useEffect(() => {
    fetchNvaptVersions().then(v => {
      setNv(v)
      if (v.drivers?.length) setNvDriver(v.drivers[0].version)
      if (v.toolkits?.length) setNvToolkit(v.toolkits[0].package)
    }).catch(e => setNv({ available: true, drivers: [], toolkits: [], error: String(e.message || e) }))
      .finally(() => setNvLoading(false))
  }, [])

  // Tool-package groups (bundle selected ones into the ISO).
  useEffect(() => {
    fetchToolpkgs().then(d => {
      setToolGroups(d.groups || [])
      setToolStatus(d.status || [])
    }).catch(() => {})
  }, [])

  // DOCA-OFED info (current OFED version it provides).
  useEffect(() => {
    fetchDocaInfo().then(setDoca).catch(() => setDoca({ available: false }))
  }, [])

  const toggleTool = name =>
    setSelectedTools(cur => cur.includes(name) ? cur.filter(n => n !== name) : [...cur, name])
  const debCountOf = name => (toolStatus.find(s => s.group === name) || {}).deb_count || 0
  const allToolsSelected = toolGroups.length > 0 && selectedTools.length === toolGroups.length
  const toggleAllTools = () =>
    setSelectedTools(allToolsSelected ? [] : toolGroups.map(g => g.name))

  const selection = useMemo(() => ({
    os,
    system_version: sysVer,
    include_nvidia: includeNvidia,
    nv_driver: includeNvidia ? nvDriver : '',
    nv_toolkit: includeNvidia ? nvToolkit : '',
    include_ofed: includeOfed,
    ofed_source: includeOfed ? ofedSource : '',
    ofed_version: includeOfed && ofedSource === 'mlnx' ? ofedVer : '',
    tool_groups: selectedTools,
  }), [os, sysVer, includeNvidia, nvDriver, nvToolkit, includeOfed, ofedSource, ofedVer, selectedTools])

  // Preview auto-name + missing assets when the selection changes.
  useEffect(() => {
    if (!sysVer) return
    fetchDefaultName(selection)
      .then(r => { setDefaultName(r.default_name); setMissing(r.missing || []) })
      .catch(() => { setDefaultName(''); setMissing([]) })
  }, [selection, sysVer])

  const osOptions = [...new Set(catalog.base_images.map(b => b.os))].map(o => ({ value: o, label: o }))
  const verOptions = catalog.base_images.filter(b => b.os === os).map(b => ({ value: b.version, label: b.version }))
  const driverOptions = (nv.drivers || []).map(d => ({ value: d.version, label: cleanDriver(d.version) }))
  const toolkitOptions = (nv.toolkits || []).map(t => ({ value: t.package, label: `CUDA ${t.label}` }))
  const ofedOptions = catalog.ofed
    .filter((o, i, arr) => arr.findIndex(x => x.version === o.version) === i)
    .map(o => ({ value: o.version, label: o.version }))

  const onSubmit = async () => {
    setSubmitting(true); setError('')
    try {
      await submitBuild({ ...selection, name: name.trim() })
      setName('')
      onSubmitted?.()
    } catch (e) {
      setError(String(e.message || e))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <Card>
        <CardHeader title="新建构建" description="基础镜像、NVIDIA 驱动、CUDA、OFED 版本均可独立选择，缺失资产自动在线下载" />
        <CardBody className="space-y-6">
          {/* 基础系统 */}
          <section className="space-y-3">
            <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">基础系统</h3>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="发行版">
                <Select value={os} onChange={setOs} options={osOptions.length ? osOptions : [{ value: 'ubuntu', label: 'ubuntu' }]} />
              </Field>
              <Field label="系统版本">
                <Select value={sysVer} onChange={setSysVer} options={verOptions.length ? verOptions : [{ value: sysVer, label: sysVer || '加载中…' }]} />
              </Field>
            </div>
          </section>

          {/* 驱动 + OFED，横向两栏 */}
          <div className="grid gap-4 lg:grid-cols-2">
            <div className="space-y-3 rounded-lg border bg-secondary/30 p-4">
              <Checkbox checked={includeNvidia} onChange={setIncludeNvidia} label="包含 NVIDIA 驱动 / CUDA" />
              {includeNvidia && (
                <>
                  {nv.available === false && <Alert variant="warning">当前主机无 apt，驱动版本仅 Linux 服务器可列。</Alert>}
                  {nv.error && <Alert variant="warning">{nv.error}</Alert>}
                  <div className="grid gap-4 sm:grid-cols-2">
                    <Field label="NVIDIA 驱动版本" hint="独立选择，缺失自动下载">
                      <Select value={nvDriver} onChange={setNvDriver}
                        options={driverOptions.length ? driverOptions : [{ value: '', label: nvLoading ? '加载中…' : '（无）' }]} />
                    </Field>
                    <Field label="CUDA 版本" hint="可与任意驱动组合">
                      <Select value={nvToolkit} onChange={setNvToolkit}
                        options={toolkitOptions.length ? toolkitOptions : [{ value: '', label: nvLoading ? '加载中…' : '（无）' }]} />
                    </Field>
                  </div>
                </>
              )}
            </div>

            <div className="space-y-3 rounded-lg border bg-secondary/30 p-4">
              <Checkbox checked={includeOfed} onChange={setIncludeOfed} label="包含 OFED / InfiniBand" />
              {includeOfed && (
                <>
                  <Field label="OFED 来源" hint="DOCA-OFED 是当前维护版；MLNX_OFED 已停更（≤24.10）">
                    <Select value={ofedSource} onChange={setOfedSource}
                      options={[
                        { value: 'doca', label: `DOCA-OFED（新，${doca?.ofed_version ? 'OFED ' + doca.ofed_version : 'DOCA ' + (doca?.version || '3.5.0')}）` },
                        { value: 'mlnx', label: 'MLNX_OFED（旧，已停更）' },
                      ]} />
                  </Field>
                  {ofedSource === 'mlnx' && (
                    <Field label="MLNX_OFED 版本" hint="按系统版本匹配对应 Ubuntu 包">
                      <Select value={ofedVer} onChange={setOfedVer}
                        options={ofedOptions.length ? ofedOptions : [{ value: '', label: '加载中…' }]} />
                    </Field>
                  )}
                  {ofedSource === 'doca' && doca?.available === false && (
                    <Alert variant="warning">当前主机无 apt，DOCA-OFED 仅 Linux 服务器可用。</Alert>
                  )}
                </>
              )}
            </div>
          </div>

          {/* 常用工具包 */}
          <section className="space-y-3 rounded-lg border bg-secondary/30 p-4">
            <div className="flex items-center justify-between">
              <div>
                <h3 className="text-sm font-medium">常用工具包（打进镜像离线安装）</h3>
                <p className="text-xs text-muted-foreground">勾选的分组构建时确保已下载（缺失自动拉取依赖闭包）</p>
              </div>
              <label className="flex shrink-0 cursor-pointer items-center gap-2 text-sm font-medium">
                <input type="checkbox" className="h-4 w-4 rounded border-input accent-primary"
                  checked={allToolsSelected} onChange={toggleAllTools} />
                全选（{selectedTools.length}/{toolGroups.length}）
              </label>
            </div>
            {toolGroups.length === 0 && <p className="text-xs text-muted-foreground">加载中…</p>}
            <div className="grid gap-x-4 gap-y-2 sm:grid-cols-2 lg:grid-cols-3">
              {toolGroups.map(g => {
                const n = debCountOf(g.name)
                return (
                  <label key={g.name} className="flex cursor-pointer items-start gap-2 rounded-md border border-transparent p-2 text-sm hover:border-border hover:bg-accent/40">
                    <input type="checkbox" className="mt-0.5 h-4 w-4 rounded border-input accent-primary"
                      checked={selectedTools.includes(g.name)} onChange={() => toggleTool(g.name)} />
                    <span>
                      <span className="font-medium">{g.title}</span>
                      {n > 0 && <span className="ml-1 text-xs text-success">已下 {n}</span>}
                      <span className="block text-xs text-muted-foreground">{g.description}</span>
                    </span>
                  </label>
                )
              })}
            </div>
          </section>

          {/* ISO 名称 + 提交 */}
          <div className="grid items-end gap-4 lg:grid-cols-[1fr_auto]">
            <Field label="ISO 名称" hint={`留空则自动命名：${defaultName || '（生成中）'}`}>
              <Input value={name} onChange={setName} placeholder={defaultName || '自动生成'} />
            </Field>
            <button className="btn-primary h-10 px-8" onClick={onSubmit} disabled={submitting}>
              {submitting ? '提交中…' : '开始构建'}
            </button>
          </div>

          {missing.length > 0 && (
            <Alert variant={missing.some(m => !m.downloadable) ? 'warning' : 'info'}
              title={missing.some(m => !m.downloadable) ? '部分资产需手动放入' : '以下资产将在线下载'}>
              <ul className="list-disc space-y-1 pl-5">
                {missing.map((m, i) => <li key={i}>{m.note}</li>)}
              </ul>
            </Alert>
          )}

          {error && <Alert variant="error">{error}</Alert>}
        </CardBody>
      </Card>
    </div>
  )
}
