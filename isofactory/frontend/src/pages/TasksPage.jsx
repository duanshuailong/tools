import { useEffect, useState, useCallback } from 'react'
import { listJobs, fetchLog, downloadURL } from '../api.js'
import { Card, CardHeader, CardBody, Badge, Progress, Alert } from '../components/ui.jsx'

const STATUS_LABEL = { queued: '排队中', running: '构建中', done: '已完成', failed: '失败' }
const PHASE_LABEL = {
  queued: '排队中', resolving: '解析资产', downloading: '下载资产',
  building: '打包镜像', archiving: '归档产物', done: '完成', failed: '失败',
}

export default function TasksPage() {
  const [jobs, setJobs] = useState([])
  const [selected, setSelected] = useState(null)
  const [log, setLog] = useState('')

  const refresh = useCallback(async () => {
    try { setJobs(await listJobs()) } catch { /* transient */ }
  }, [])

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, 2000)
    return () => clearInterval(t)
  }, [refresh])

  useEffect(() => {
    if (!selected) return
    let alive = true
    const load = async () => {
      try { const t = await fetchLog(selected); if (alive) setLog(t) } catch { /**/ }
    }
    load()
    const t = setInterval(load, 2000)
    return () => { alive = false; clearInterval(t) }
  }, [selected])

  return (
    <div className="mx-auto max-w-5xl space-y-6">
      <Card>
        <CardHeader title="构建任务" description="实时状态与进度，完成后可下载 ISO" />
        <CardBody className="space-y-3">
          {jobs.length === 0 && <p className="text-sm text-muted-foreground">暂无任务</p>}
          {jobs.map(j => (
            <div key={j.id} className="rounded-md border p-4">
              <div className="flex flex-wrap items-center gap-3">
                <Badge status={j.status}>{STATUS_LABEL[j.status] || j.status}</Badge>
                <span className="font-mono text-sm text-muted-foreground">{j.id}</span>
                <span className="flex-1 truncate font-mono text-sm">{j.output_name || '—'}</span>
                <button className="btn-outline h-8 px-3 text-xs" onClick={() => setSelected(selected === j.id ? null : j.id)}>
                  {selected === j.id ? '收起日志' : '日志'}
                </button>
                {j.has_iso && <a className="btn-primary h-8 px-3 text-xs" href={downloadURL(j.id)}>下载 ISO</a>}
              </div>

              {j.status === 'running' && (
                <div className="mt-3 space-y-1.5">
                  <Progress percent={j.percent} indeterminate={j.percent < 0} />
                  <p className="text-xs text-muted-foreground">
                    {PHASE_LABEL[j.phase] || j.phase}
                    {j.percent >= 0 ? ` · ${j.percent.toFixed(0)}%` : ''}
                    {j.phase_msg ? ` · ${j.phase_msg}` : ''}
                  </p>
                </div>
              )}
              {j.status === 'failed' && j.error && (
                <p className="mt-2 text-xs text-destructive">{j.error}</p>
              )}

              {selected === j.id && (
                <pre className="terminal mt-3 max-h-96 whitespace-pre-wrap break-all">
                  {log || '（暂无输出）'}
                </pre>
              )}
            </div>
          ))}
        </CardBody>
      </Card>
    </div>
  )
}
