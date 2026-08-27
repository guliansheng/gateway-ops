import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes } from 'react-router-dom'
import '@fontsource-variable/geist'
import '@fontsource-variable/geist-mono'
import { ThemeProvider } from '@/components/theme-provider'
import { AuthProvider } from '@/lib/auth-context'
import { RefreshProvider } from '@/lib/refresh-context'
import { AddChannelProvider } from '@/lib/add-channel-context'
import { AuthGate } from '@/components/auth/auth-gate'
import { AppShell } from '@/components/app-shell'
import { Toaster } from '@/components/ui/sonner'
import DashboardPage from '@/app/page'
import ChannelsPage from '@/app/channels-page'
import CaptchaPage from '@/app/captcha-page'
import NotificationsPage from '@/app/notifications-page'
import RelayStationsPage from '@/app/relay-stations-page'
import OperationsCostsPage from '@/app/operations-costs-page'
import LocalPoolPage from '@/app/local-pool-page'
import PublicRelayMonitorPage from '@/app/public-relay-monitor-page'
import '@/app/globals.css'

function ProtectedApplication() {
  return (
    <AuthProvider>
      <AuthGate>
        <RefreshProvider>
          <AddChannelProvider>
            <AppShell />
          </AddChannelProvider>
        </RefreshProvider>
      </AuthGate>
    </AuthProvider>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider attribute="class" defaultTheme="light" enableSystem disableTransitionOnChange>
      <BrowserRouter>
        <Routes>
          <Route path="public/relay-monitor/:stationID" element={<PublicRelayMonitorPage />} />
          <Route element={<ProtectedApplication />}>
            <Route index element={<DashboardPage />} />
            <Route path="channels" element={<ChannelsPage />} />
            <Route path="captcha" element={<CaptchaPage />} />
            <Route path="notifications" element={<NotificationsPage />} />
            <Route path="relay-stations" element={<RelayStationsPage />} />
            <Route path="operations/costs" element={<OperationsCostsPage />} />
            <Route path="operations/local-pool" element={<LocalPoolPage />} />
          </Route>
        </Routes>
      </BrowserRouter>
      <Toaster richColors closeButton position="top-right" />
    </ThemeProvider>
  </StrictMode>,
)
