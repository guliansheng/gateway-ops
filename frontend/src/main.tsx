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
import SettingsPage from '@/app/settings-page'
import ChannelsPage from '@/app/channels-page'
import CaptchaPage from '@/app/captcha-page'
import NotificationsPage from '@/app/notifications-page'
import RelayStationsPage from '@/app/relay-stations-page'
import OperationsCostsPage from '@/app/operations-costs-page'
import LocalPoolPage from '@/app/local-pool-page'
import PublicRelayMonitorPage from '@/app/public-relay-monitor-page'
import PublicModelPricingPage from '@/app/public-model-pricing-page'
import PublicServiceStatusPage from '@/app/public-service-status-page'
import PublicHelpPage from '@/app/public-help-page'
import '@/app/globals.css'

function ProtectedApplication() {
  // A public document can navigate client-side without receiving fresh headers.
  // Never mount authentication or management UI inside that existing iframe.
  if (window.self !== window.top) {
    return <p role="alert" className="p-6 text-sm">管理后台不能在 iframe 内打开，请在独立窗口访问。</p>
  }
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
          <Route path="public/model-pricing/:stationID" element={<PublicModelPricingPage />} />
          <Route path="public/service-status" element={<PublicServiceStatusPage />} />
          <Route path="public/help/:stationID" element={<PublicHelpPage />} />
          <Route element={<ProtectedApplication />}>
            <Route index element={<DashboardPage />} />
            <Route path="channels" element={<ChannelsPage />} />
            <Route path="captcha" element={<CaptchaPage />} />
            <Route path="notifications" element={<NotificationsPage />} />
            <Route path="settings" element={<SettingsPage />} />
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
