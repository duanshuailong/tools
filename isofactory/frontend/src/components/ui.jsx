// Small shadcn/ui-style primitives, hand-built (no runtime dep) so colleagues
// can read and extend them easily. Styling comes from the Tailwind component
// classes in styles.css plus utility classes here.

export function Card({ children, className = '' }) {
  return <div className={`card ${className}`}>{children}</div>
}

export function CardHeader({ title, description, actions }) {
  return (
    <div className="flex items-start justify-between gap-4 border-b px-6 py-4">
      <div>
        <h2 className="text-base font-semibold leading-none tracking-tight">{title}</h2>
        {description && <p className="mt-1.5 text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions}
    </div>
  )
}

export function CardBody({ children, className = '' }) {
  return <div className={`px-6 py-5 ${className}`}>{children}</div>
}

export function Field({ label, hint, children }) {
  return (
    <div className="space-y-1.5">
      <label className="label">{label}</label>
      {children}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  )
}

export function Select({ value, onChange, options, disabled }) {
  return (
    <select className="input" value={value} disabled={disabled}
      onChange={e => onChange(e.target.value)}>
      {options.map(o => (
        <option key={o.value} value={o.value}>{o.label}</option>
      ))}
    </select>
  )
}

export function Input({ value, onChange, placeholder, disabled }) {
  return (
    <input className="input" value={value} placeholder={placeholder} disabled={disabled}
      onChange={e => onChange(e.target.value)} />
  )
}

export function Checkbox({ checked, onChange, label }) {
  return (
    <label className="flex cursor-pointer items-center gap-2 text-sm font-medium">
      <input type="checkbox" checked={checked} onChange={e => onChange(e.target.checked)}
        className="h-4 w-4 rounded border-input accent-primary" />
      {label}
    </label>
  )
}

const BADGE = {
  queued: 'badge-queued', running: 'badge-running',
  done: 'badge-done', failed: 'badge-failed',
}
export function Badge({ status, children }) {
  return <span className={BADGE[status] || 'badge-queued'}>{children}</span>
}

export function Progress({ percent, indeterminate }) {
  return (
    <div className="h-2 w-full overflow-hidden rounded-full bg-secondary">
      {indeterminate ? (
        <div className="progress-indeterminate h-full rounded-full bg-primary" />
      ) : (
        <div
          className="h-full rounded-full bg-primary transition-all duration-500"
          style={{ width: `${Math.max(0, Math.min(100, percent))}%` }}
        />
      )}
    </div>
  )
}

export function Alert({ variant = 'info', title, children }) {
  const styles = {
    info: 'border-primary/30 bg-primary/5 text-primary',
    warning: 'border-warning/40 bg-warning/10 text-warning',
    error: 'border-destructive/40 bg-destructive/10 text-destructive',
  }[variant]
  return (
    <div className={`rounded-md border px-4 py-3 text-sm ${styles}`}>
      {title && <p className="mb-1 font-medium">{title}</p>}
      {children}
    </div>
  )
}
