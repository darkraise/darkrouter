import { useEffect, useRef, useState } from "react"
import { Button, Sheet, SheetContent, SheetTitle, SheetTrigger } from "darkraise-ui"
import { PanelLeftOpen } from "lucide-react"
import { usePlaygroundConversation, usePlaygroundConversations } from "../../../lib/queries"
import {
  configOfConversation,
  messagesOfTurns,
  routesOfTurns,
  titleFromPrompt,
  useAppendTurn,
  useCreateConversation,
  useDeleteConversation,
  useUpdateConversation,
} from "../lib/conversations"
import { useChatRun, type CompletedTurn } from "../lib/use-chat-run"
import { emptyConfig, type PlaygroundConfig } from "../config"
import { parseTools } from "../lib/request"
import { Transcript } from "../transcript"
import { Composer } from "../composer"
import { HistoryRail } from "./history-rail"
import { ConversationHeader } from "./conversation-header"
import type { PlaygroundConversation } from "../../../lib/api-types"

/**
 * A conversation that is still there tomorrow.
 *
 * Three regions: the history rail, the transcript, and the composer pinned to
 * the foot. No config pane and no metrics strip — the two settings a
 * conversation genuinely needs are on the header, and everything else belongs
 * to Lab.
 *
 * The saving is deliberately invisible. A history rail behind an explicit Save
 * button does not get used, and a conversation the operator has to remember to
 * keep is one they will lose. What that costs is stated in spec section 8.2:
 * this is the first place darkrouter retains prompt text at rest.
 */
export function ChatMode({
  onOpenInLab,
}: {
  onOpenInLab: (config: PlaygroundConfig) => void
}) {
  const [config, setConfig] = useState<PlaygroundConfig>(emptyConfig)
  const [activeId, setActiveId] = useState("")
  const [loadedId, setLoadedId] = useState("")
  const [title, setTitle] = useState("New chat")
  const [collapsed, setCollapsed] = useState(false)
  const [railOpen, setRailOpen] = useState(false)

  const { data: conversations } = usePlaygroundConversations()
  const detail = usePlaygroundConversation(activeId, { enabled: activeId !== "" })
  const create = useCreateConversation()
  const append = useAppendTurn()
  const update = useUpdateConversation()
  const remove = useDeleteConversation()

  // The turn callback runs inside an async send, so it reads the conversation
  // and the settings through refs rather than through the closure the send
  // captured. Two messages sent in quick succession would otherwise both see
  // an empty id and create two conversations for one exchange.
  const conversationRef = useRef("")
  const configRef = useRef(config)
  configRef.current = config

  async function persistTurn(turn: CompletedTurn) {
    try {
      let id = conversationRef.current
      if (id === "") {
        const made = await create.mutateAsync({
          title: titleFromPrompt(turn.prompt),
          config: configRef.current,
        })
        id = made.id
        conversationRef.current = id
        setActiveId(id)
        // Marked loaded at creation, so the read below does not fetch the row
        // that was just written and replace the live transcript with it.
        setLoadedId(id)
        setTitle(made.title)
      }
      await append.mutateAsync({ id, role: "user", content: turn.prompt, requestId: "" })
      await append.mutateAsync({
        id, role: "assistant", content: turn.answer, requestId: turn.requestId,
      })
    } catch {
      // useApiMutation has already reported it through the toaster. Losing a
      // saved turn must not take the transcript on screen down with it.
    }
  }

  const run = useChatRun(config, () => {}, (turn) => void persistTurn(turn))

  // Applied once per conversation: re-firing would stomp on turns the operator
  // has typed since it was opened.
  useEffect(() => {
    if (!detail.data || detail.data.id === loadedId) return
    run.load(messagesOfTurns(detail.data.messages), routesOfTurns(detail.data.messages))
    setConfig(configOfConversation(detail.data))
    setTitle(detail.data.title)
    setLoadedId(detail.data.id)
  }, [detail.data, loadedId])

  function startNew() {
    conversationRef.current = ""
    setActiveId("")
    setLoadedId("")
    setTitle("New chat")
    run.load([], {})
    setRailOpen(false)
  }

  function select(id: string) {
    if (id === activeId) return
    conversationRef.current = id
    setActiveId(id)
    setRailOpen(false)
  }

  /** Model, dialect and the system prompt are stored with the conversation, so
   *  changing one part-way through moves the row rather than only the screen.
   *  The turns that came before stay: each answer's route line already records
   *  what actually served it. */
  function changeConfig(next: PlaygroundConfig) {
    setConfig(next)
    if (conversationRef.current !== "") {
      update.mutate({ id: conversationRef.current, title, config: next })
    }
  }

  function retitle(next: string) {
    setTitle(next)
    if (conversationRef.current !== "") {
      update.mutate({ id: conversationRef.current, title: next, config })
    }
  }

  function removeConversation(c: PlaygroundConversation) {
    remove.mutate({ id: c.id, title: c.title })
    if (c.id === conversationRef.current) startNew()
  }

  const rail = (
    <HistoryRail
      conversations={conversations ?? []}
      activeId={activeId}
      onSelect={select}
      onNew={startNew}
      onDelete={removeConversation}
      collapsed={collapsed}
      onToggleCollapsed={() => setCollapsed((c) => !c)}
    />
  )

  return (
    <div className="flex min-h-0 flex-1">
      {/* A 260px rail beside a transcript is two columns on a laptop and a
          squeeze on anything narrower, so below lg it becomes a sheet. */}
      <div className="hidden lg:flex">{rail}</div>

      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        <div className="flex items-center gap-2 lg:hidden">
          <Sheet open={railOpen} onOpenChange={setRailOpen}>
            <SheetTrigger asChild>
              <Button variant="ghost" size="icon" aria-label="Show conversations" className="ml-4 mt-2">
                <PanelLeftOpen className="size-[var(--icon-size)]" aria-hidden="true" />
              </Button>
            </SheetTrigger>
            <SheetContent side="left" className="w-[280px] p-0">
              <SheetTitle className="sr-only">Conversations</SheetTitle>
              <HistoryRail
                conversations={conversations ?? []}
                activeId={activeId}
                onSelect={select}
                onNew={startNew}
                onDelete={removeConversation}
                collapsed={false}
                onToggleCollapsed={() => setRailOpen(false)}
              />
            </SheetContent>
          </Sheet>
        </div>

        <ConversationHeader
          config={config}
          onConfigChange={changeConfig}
          title={title}
          onTitleChange={retitle}
          onOpenInLab={() => onOpenInLab(config)}
          onDelete={() => {
            const current = (conversations ?? []).find((c) => c.id === activeId)
            if (current) removeConversation(current)
          }}
          canDelete={activeId !== ""}
        />

        {/* Centred and capped: a transcript run to the full width of a wide
            monitor is a line length nobody reads twice. */}
        <div className="mx-auto flex min-h-0 w-full max-w-3xl flex-1 flex-col">
          <Transcript
            messages={run.messages}
            routes={run.routes}
            busy={run.busy}
            model={config.model}
            quiet
          />
          <Composer
            model={config.model}
            busy={run.busy}
            error={run.error}
            toolsError={parseTools(config.toolsRaw).error}
            canClear={run.messages.length > 0}
            onSend={(p) => void run.send(p)}
            onStop={run.stop}
            onClear={startNew}
          />
        </div>
      </div>
    </div>
  )
}
