import { useState, type FormEvent, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { tokenStore } from './api'
import { pages } from './pages'

export default function App({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const [token, setToken] = useState(tokenStore.get())
  const [entry, setEntry] = useState(token)

  function login(event: FormEvent) {
    event.preventDefault()
    const next = entry.trim()
    tokenStore.set(next)
    queryClient.clear()
    setToken(next)
  }

  function logout() {
    tokenStore.set('')
    queryClient.clear()
    setToken('')
    setEntry('')
  }

  if (!token) {
    return <main className="login"><form onSubmit={login}>
      <div className="brand">vLLM <i>Use</i></div>
      <h1>管理控制台</h1>
      <p>输入启动时配置的管理员令牌，或 <code>admin-bootstrap.token</code> 内容。</p>
      <label><span>管理员令牌</span><input autoFocus required type="password" value={entry} onChange={(event) => setEntry(event.target.value)} autoComplete="current-password" /></label>
      <button disabled={!entry.trim()}>进入</button>
      <small>令牌仅保存在本标签页的 sessionStorage。</small>
    </form></main>
  }

  return <div className="shell"><aside>
    <div className="brand">vLLM <i>Use</i></div>
    <nav aria-label="主导航">{pages.map((page) => <Link key={page.path} to={page.path} activeProps={{ className: 'active' }}>{page.label}</Link>)}</nav>
    <button className="logout" onClick={logout}>退出</button>
  </aside><main className="content">{children}</main></div>
}
