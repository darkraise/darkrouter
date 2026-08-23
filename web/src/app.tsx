import { ThemeProvider } from "darkraise-ui/theme"
import { SidebarLayout } from "darkraise-ui/layout"
import { themeConfig } from "./theme.config"

export function App() {
  return (
    <ThemeProvider config={themeConfig}>
      <SidebarLayout
        nav={[]}
        showThemeSwitcher={true}
      >
        <div className="flex flex-col items-center justify-center gap-4 py-16">
          <h1 className="text-4xl font-medium">Welcome</h1>
          <p className="text-muted-foreground">
            Your project is ready. Start building in src/app.tsx
          </p>
        </div>
      </SidebarLayout>
    </ThemeProvider>
  )
}
