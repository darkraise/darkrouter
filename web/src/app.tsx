import { useEffect, useState } from "react"
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query"
import { ThemeProvider } from "darkraise-ui/theme"
import { Toaster } from "darkraise-ui"
import { themeConfig } from "./theme.config"
import { api, isTransient, onUnauthorized, setCsrfToken } from "./lib/api"
import { RouterProvider } from "@tanstack/react-router"
import { LoginScreen } from "./routes/login"
import { FirstRun } from "./features/shell/first-run"
import { router } from "./lib/router"

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Spec §5: polling, paused when the tab is hidden. A dashboard nobody is
      // looking at should not keep a gateway busy.
      refetchOnWindowFocus: true,
      refetchIntervalInBackground: false,
      // Only a failure the next attempt might not repeat. A 401 retried
      // three times delays the login screen; a 404 or 400 comes back the
      // same however often it is asked.
      retry: (count, err) => isTransient(err) && count < 2,
    },
  },
})

type AuthStatus = {
  authenticated: boolean
  /** Whether an admin password exists at all. */
  configured: boolean
  csrf_token?: string
}

function Shell() {
  // Not whether the operator is authenticated -- that is the status call's
  // answer -- but whether a later call has since found the session gone. The
  // cleared cache alone cannot say so: an undefined status reads the same
  // before the first call as after a logout, and one renders nothing while
  // the other has to render the login form.
  const [revoked, setRevoked] = useState(false)

  const status = useQuery({
    queryKey: ["auth-status"],
    queryFn: () => api.get<AuthStatus>("/api/auth/status"),
  })

  useEffect(() => {
    if (status.data?.csrf_token) setCsrfToken(status.data.csrf_token)
  }, [status.data])

  useEffect(
    () =>
      onUnauthorized(() => {
        // Any call anywhere can discover the session is gone. One listener
        // beats every screen handling it.
        setRevoked(true)
        queryClient.clear()
      }),
    [],
  )

  const authed = revoked ? false : status.data ? status.data.authenticated : null

  if (authed === null) return null
  if (!authed) {
    // A login form nobody can pass is worse than useless: it refuses every
    // password and says nothing about why.
    if (status.data && !status.data.configured) return <FirstRun />
    return (
      <LoginScreen
        onAuthenticated={() => {
          setRevoked(false)
          void status.refetch()
        }}
      />
    )
  }
  // The shell is the root route's component rather than a wrapper here, so
  // that darkraise-ui's sidebar has the router context its adapter needs.
  return <RouterProvider router={router} />
}

export function App() {
  return (
    <ThemeProvider config={themeConfig}>
      <QueryClientProvider client={queryClient}>
        <Shell />
        {/* Outside Shell so a failure raised while the login screen is up has
            somewhere to land too. */}
        <Toaster />
      </QueryClientProvider>
    </ThemeProvider>
  )
}
