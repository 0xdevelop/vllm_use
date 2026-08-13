import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { render, screen } from '@testing-library/react'
import { fireEvent } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import App from './App'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}))

describe('admin authentication', () => {
  it('stores the bootstrap token and reveals navigation', () => {
    render(<QueryClientProvider client={new QueryClient()}><App><p>内容</p></App></QueryClientProvider>)
    fireEvent.change(screen.getByLabelText('管理员令牌'), { target: { value: 'bootstrap-secret' } })
    fireEvent.click(screen.getByRole('button', { name: '进入' }))
    expect(screen.getByRole('navigation')).toBeInTheDocument()
    expect(sessionStorage.getItem('vllm-use-admin-token')).toBe('bootstrap-secret')
  })
})
