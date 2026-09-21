import { useState, useEffect } from 'react'
import { preflight } from './api.js'
import BuildPage from './pages/BuildPage.jsx'
import TasksPage from './pages/TasksPage.jsx'
import CompletedPage from './pages/CompletedPage.jsx'

const NAV = [
  { id: 'build', label: '新建构建', icon: '⊕' },
  { id: 'tasks', label: '构建任务', icon: '☰' },
  { id: 'completed', label: '已完成镜像', icon: '✓' },
]

export default function App() {
  const [page, setPage] = useState('build')
  const [ready, setReady] = useState(null)

  useEffect(() => {
    preflight().then(setReady).catch(() => setReady({ ready: false, reason: '无法连接后端' }))
  }, [])

  return (
    <div className="flex min-h-screen">
      {/* Sidebar */}
      <aside className="flex w-56 flex-col border-r bg-card">
        <div className="flex items-center gap-2 border-b px-5 py-4">
          <div className="flex h-8 w-8 items-center justify-center rounded-md bg-primary text-sm font-bold text-primary-foreground">Iso</div>
          <div>
            <div className="text-sm font-semibold leading-none">IsoFactory</div>
            <div className="mt-1 text-xs text-muted-foreground">ISO 制作平台</div>
          </div>
        </div>
        <nav className="flex-1 space-y-1 p-3">
          {NAV.map(n => (
            <button key={n.id} onClick={() => setPage(n.id)}
              className={`flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors ${
                page === n.id ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground'
              }`}>
              <span className="text-base">{n.icon}</span>{n.label}
            </button>
          ))}
        </nav>
        <div className="border-t px-4 py-3 text-xs text-muted-foreground">
          {ready == null ? '检测中…'
            : ready.ready ? <span className="text-success">● 服务就绪</span>
            : <span className="text-destructive">● {ready.reason}</span>}
        </div>
      </aside>

      {/* Main */}
      <main className="flex-1 overflow-auto">
        <header className="sticky top-0 z-10 border-b bg-background/80 px-8 py-4 backdrop-blur">
          <h1 className="text-lg font-semibold">{NAV.find(n => n.id === page)?.label}</h1>
        </header>
        <div className="p-8">
          {page === 'build' && <BuildPage onSubmitted={() => setPage('tasks')} />}
          {page === 'tasks' && <TasksPage />}
          {page === 'completed' && <CompletedPage />}
        </div>
      </main>
    </div>
  )
}
