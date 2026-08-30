import { useState } from "react"
import { useNavigate, useSearch } from "@tanstack/react-router"
import { PageHeader } from "darkraise-ui/layout"
import { ToggleGroup, ToggleGroupItem } from "darkraise-ui"
import { LabMode } from "./lab-mode"
import { emptyConfig, type PlaygroundConfig } from "./config"
import { initialMode, isMode, rememberMode, type PlaygroundMode } from "./mode"

/**
 * The playground, as two modes rather than one crowded instrument.
 *
 * Lab is the instrument: four surfaces, a config pane, and the readings from
 * the last run. Chat is a conversation that is still there tomorrow. They are
 * separate modes because one screen serving both meant a settings column
 * taking a fifth of the width from a reader, and a transcript that vanished on
 * reload for everyone else.
 *
 * The chosen mode is remembered per operator and carried in the URL, so a
 * shared link opens where its sender meant rather than where its reader last
 * was.
 */
export function PlaygroundScreen() {
  const search = useSearch({ strict: false })
  const navigate = useNavigate()
  const [mode, setMode] = useState<PlaygroundMode>(() => initialMode(search))
  // Lab's request settings live here rather than inside LabMode so that Chat
  // mode's "open this configuration in Lab" is a state change and a mode
  // switch, rather than a value smuggled across through storage or the URL.
  const [labConfig, setLabConfig] = useState<PlaygroundConfig>(emptyConfig)

  function choose(next: string) {
    if (!isMode(next) || next === mode) return
    setMode(next)
    rememberMode(next)
    void navigate({ to: "/playground", search: (prev) => ({ ...prev, mode: next }) })
  }

  return (
    <div className="-m-6 flex h-[calc(100%+3rem)] min-h-0 flex-col">
      <PageHeader
        title="Playground"
        description="Send a real request, and see what it cost"
        actions={
          <ToggleGroup type="single" value={mode} onValueChange={choose} aria-label="Playground mode">
            <ToggleGroupItem value="chat">Chat</ToggleGroupItem>
            <ToggleGroupItem value="lab">Lab</ToggleGroupItem>
          </ToggleGroup>
        }
      />
      {mode === "chat" ? null : <LabMode config={labConfig} onConfigChange={setLabConfig} />}
    </div>
  )
}
