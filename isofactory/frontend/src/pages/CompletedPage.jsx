import { useEffect, useState, useCallback } from 'react'
import { listJobs, downloadURL } from '../api.js'
import { Card, CardHeader, CardBody } from '../components/ui.jsx'

// Lists only successfully-built ISOs, for quick download.
export default function CompletedPage() {
  const [jobs, setJobs] = useState([])

  const refresh = useCallback(async () => {
    try {
      const all = await listJobs()
      setJobs(all.filter(j => j.status === 'done' && j.has_iso))
    } catch { /* transient */ }
  }, [])

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, 3000)
    return () => clearInterval(t)
  }, [refresh])

  return (
    <div className="mx-auto max-w-5xl space-y-6">
      <Card>
        <CardHeader title="已完成镜像" description="构建成功、可直接下载的 ISO" />
        <CardBody className="space-y-2">
          {jobs.length === 0 && <p className="text-sm text-muted-foreground">暂无已完成的镜像</p>}
          {jobs.map(j => (
            <div key={j.id} className="flex flex-wrap items-center gap-3 rounded-md border p-4">
              <span className="font-mono text-sm">{j.output_name || j.id}</span>
              <span className="text-xs text-muted-foreground">{j.finished || j.queued}</span>
              <a className="btn-primary ml-auto h-8 px-3 text-xs" href={downloadURL(j.id)}>下载 ISO</a>
            </div>
          ))}
        </CardBody>
      </Card>
    </div>
  )
}
