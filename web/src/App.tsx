import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { APIError, tokenStore, validateManagementToken } from './api'
import { pages } from './pages'

export default function App({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const [restoredToken] = useState(() => tokenStore.get())
  const [token, setToken] = useState('')
  const [entry, setEntry] = useState(restoredToken)
  const [checkingSession, setCheckingSession] = useState(Boolean(restoredToken))
  const [authenticating, setAuthenticating] = useState(false)
  const [authError, setAuthError] = useState('')

  useEffect(() => {
    if (!restoredToken) return
    let active = true
    void validateManagementToken(restoredToken).then(() => {
      if (!active) return
      setToken(restoredToken)
      setCheckingSession(false)
    }).catch((error: unknown) => {
      if (!active) return
      queryClient.clear()
      if (error instanceof APIError && (error.status === 401 || error.status === 403)) {
        tokenStore.set('')
        setEntry('')
        setAuthError('已保存的会话已失效，请重新输入令牌')
      } else {
        setAuthError('暂时无法验证已保存的会话，请重试')
      }
      setCheckingSession(false)
    })
    return () => { active = false }
  }, [queryClient, restoredToken])

  async function login(event: FormEvent) {
    event.preventDefault()
    const next = entry.trim()
    setAuthenticating(true)
    setAuthError('')
    try {
      await validateManagementToken(next)
      tokenStore.set(next)
      queryClient.clear()
      setToken(next)
    } catch (error: unknown) {
      tokenStore.set('')
      setAuthError(error instanceof APIError && (error.status === 401 || error.status === 403)
        ? '令牌无效或没有管理权限'
        : '无法连接管理服务，请稍后重试')
    } finally {
      setAuthenticating(false)
    }
  }

  function logout() {
    tokenStore.set('')
    queryClient.clear()
    setToken('')
    setEntry('')
    setAuthError('')
  }

  if (checkingSession) {
    return <main className="login"><div className="loading-session" role="status">正在验证已保存的会话…</div></main>
  }

  if (!token) {
    return <main className="login"><form onSubmit={(event) => { void login(event) }}>
      <div className="brand">vLLM <i>Use</i></div>
      <h1>管理控制台</h1>
      <p>输入启动时配置的管理员令牌、具备管理 scope 的 API key，或 <code>admin-bootstrap.token</code> 内容。</p>
      {authError && <div className="notice error" role="alert">{authError}</div>}
      <label><span>管理员令牌</span><input autoFocus required disabled={authenticating} type="password" value={entry} onChange={(event) => setEntry(event.target.value)} autoComplete="current-password" /></label>
      <button disabled={authenticating || !entry.trim()}>{authenticating ? '正在验证…' : '进入'}</button>
      <small>验证成功后，令牌仅保存在本标签页的 sessionStorage。</small>
    </form></main>
  }

  return <div className="shell"><aside>
    <div className="brand">vLLM <i>Use</i></div>
    <nav aria-label="主导航">{pages.map((page) => <Link key={page.path} to={page.path} activeProps={{ className: 'active' }}>{page.label}</Link>)}</nav>
    <button className="logout" onClick={logout}>退出</button>
  </aside><main className="content">{children}</main></div>
}
