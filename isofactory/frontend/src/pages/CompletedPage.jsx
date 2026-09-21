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
        <CardBody>
          {jobs.length === 0 ? (
            <p className="text-sm text-muted-foreground">暂无已完成的镜像</p>
          ) : (
            <div className="overflow-x-auto rounded-md border">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b bg-secondary/50 text-left text-xs text-muted-foreground">
                    <th className="px-4 py-2.5 font-medium">ISO 名称</th>
                    <th className="px-4 py-2.5 font-medium">任务 ID</th>
                    <th className="px-4 py-2.5 font-medium">完成时间</th>
                    <th className="px-4 py-2.5 text-right font-medium">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {jobs.map(j => (
                    <tr key={j.id} className="border-b last:border-0 hover:bg-accent/40">
                      <td className="px-4 py-2.5 font-mono">{j.output_name || j.id}</td>
                      <td className="px-4 py-2.5 font-mono text-xs text-muted-foreground">{j.id}</td>
                      <td className="px-4 py-2.5 text-xs text-muted-foreground">{j.finished || j.queued}</td>
                      <td className="px-4 py-2.5 text-right">
                        <a className="btn-primary h-8 px-3 text-xs" href={downloadURL(j.id)}>下载 ISO</a>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardBody>
      </Card>
    </div>
  )
}
