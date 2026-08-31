import { useEffect, useState } from "react"
import { useNavigate, useSearch } from "@tanstack/react-router"
import { PageHeader } from "darkraise-ui/layout"
import { ToggleGroup, ToggleGroupItem } from "darkraise-ui"
import { LabMode } from "./lab-mode"
import { ChatMode } from "./chat/chat-mode"
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

  // The seed above runs once, and the router does not remount a route when
  // only its search changes -- so without this, Back after a mode switch moves
  // the URL to ?mode=chat and leaves Lab on screen.
  //
  // It sets the mode rather than routing through choose(). Back is the
  // browser restoring a location, not the operator picking a mode, so it must
  // neither write the preference nor navigate -- and navigating would push
  // ?mode= back onto a bare /playground and undo the Back that just happened.
  // initialMode rather than isMode, so Back to a bare /playground lands where
  // a fresh load of that same URL would.
  useEffect(() => {
    setMode(initialMode(search))
    // search is a fresh object each render; its two fields are what matter.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [search.mode, search.seed])

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
      {mode === "chat" ? (
        <ChatMode
          onOpenInLab={(next) => {
            setLabConfig(next)
            choose("lab")
          }}
        />
      ) : (
        <LabMode config={labConfig} onConfigChange={setLabConfig} />
      )}
    </div>
  )
}
