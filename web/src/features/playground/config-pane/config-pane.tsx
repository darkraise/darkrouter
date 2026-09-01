import type { ReactNode } from "react"
import {
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Textarea,
} from "darkraise-ui"
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "darkraise-ui"
import { ModelCombobox, useModelCandidates } from "../../shell/model-combobox"
import { DIALECTS, type PlaygroundConfig } from "../config"
import { parseTools } from "../lib/request"
import type { PlaygroundDialect } from "../../../lib/api-types"
import { Lock } from "lucide-react"
import { PresetPicker } from "./preset-picker"
import { Sampling } from "./sampling"
import { Reasoning } from "./reasoning"
import { StructuredOutput } from "./structured-output"

/**
 * The request, beside the surfaces that send it.
 *
 * A column rather than a row across the top: these are seven controls that are
 * set once and read many times, and a chat transcript needs the width more
 * than they do.
 *
 * `locked` disables every control while leaving all of them, and the sections
 * that hold them, readable. That distinction is the whole point: a conversation
 * whose settings are fixed still has to be able to say what they were fixed to,
 * and a pane that collapsed into a summary would answer "what is this set to?"
 * with a different layout from the one the operator set it in.
 *
 * The lock is drawn as a `fieldset` per section rather than as a `disabled`
 * prop threaded into Sampling, Reasoning and the rest. `fieldset[disabled]`
 * disables every form control under it whatever the nesting, so a control
 * added to any of those files later is locked without anyone remembering to
 * pass it the flag. The accordion triggers stay outside the fieldsets, which
 * is what keeps a locked pane openable.
 *
 * It renders its fields and nothing around them. The panel it sits in belongs
 * to the screen: Chat gives it a card matching the readings above it, Compare
 * a bordered column beside the grid, and the new-conversation dialog no
 * chrome at all. A component that drew its own `aside` could only ever look
 * right in one of the three.
 */
export function ConfigPane({
  config,
  onChange,
  locked = false,
  lockNote,
  showModel = true,
  showDialect = true,
  showHeading = true,
}: {
  config: PlaygroundConfig
  onChange: (next: PlaygroundConfig) => void
  /** Set once the settings have been committed to by a request. */
  locked?: boolean
  /** Why the controls do not answer, when it is not the default reason. Chat's
   *  pane never edits, so it is locked before any turn has fixed anything and
   *  the sentence about a first message would be describing something that
   *  has not happened. */
  lockNote?: string
  /** Off where the surface names its own model — Chat has a model island, and
   *  Compare names one per column. */
  showModel?: boolean
  showDialect?: boolean
  /** Off inside the dialog, whose own title already names what these are. */
  showHeading?: boolean
}) {
  const { candidates, loading } = useModelCandidates()
  const set = <K extends keyof PlaygroundConfig>(key: K, value: PlaygroundConfig[K]) =>
    onChange({ ...config, [key]: value })
  const toolsError = parseTools(config.toolsRaw).error

  return (
    <div className="flex min-w-0 flex-col gap-4">
      {/* Model and dialect decide where a prompt goes, so they stay in view.
          Sampling and the prompt scaffolding are set once a session and fold
          away — the transcript needs the height more than they do. */}
      {showHeading ? (
        <div className="flex items-center justify-between gap-2">
          <h2 className="text-sm font-medium">Request</h2>
          {/* The badge belongs to the default reason, which is the only one
              that is final. A caller supplying its own note is describing a
              softer lock — Chat's pane does not edit, but the settings behind
              it are still open — and "Fixed" beside that note would contradict
              the sentence under it. */}
          {locked && lockNote === undefined ? (
            <span className="flex items-center gap-1.5 text-sm text-[hsl(var(--legend))]">
              <Lock className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
              Fixed
            </span>
          ) : null}
        </div>
      ) : null}

      {locked ? (
        // Said rather than left to be discovered by clicking a field that
        // does not respond. What fixes the settings is the first turn: every
        // answer already in the transcript was produced under them, and
        // changing one now would make the conversation a record of a request
        // that was never sent.
        <p className="text-sm text-[hsl(var(--muted-foreground))]">
          {lockNote ??
            "Set by the first message. Start a new conversation to send under different settings."}
        </p>
      ) : null}

      <Fieldset locked={locked}>
        <PresetPicker config={config} onChange={onChange} />

        {showModel ? (
          // Labelled visibly, not only for a screen reader. It sits between
          // Preset and Dialect, and a bare field between two labelled ones
          // reads as a label that failed to render.
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="pg-model">Model or alias</Label>
            <ModelCombobox
              id="pg-model"
              label="Model or alias"
              value={config.model}
              onChange={(model) => set("model", model)}
              candidates={candidates}
              loading={loading}
              className="w-full"
            />
          </div>
        ) : null}

        {showDialect ? (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="pg-dialect">Dialect</Label>
            <Select
              value={config.dialect}
              onValueChange={(v) => set("dialect", v as PlaygroundDialect)}
            >
              <SelectTrigger id="pg-dialect">
                <SelectValue placeholder="dialect" />
              </SelectTrigger>
              <SelectContent>
                {DIALECTS.map((d) => (
                  <SelectItem key={d} value={d}>
                    {d}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        ) : null}
      </Fieldset>

      <Accordion type="multiple" defaultValue={[]} className="flex flex-col">
      <AccordionItem value="sampling">
      <AccordionTrigger className="text-sm">Sampling</AccordionTrigger>
      <AccordionContent className="pt-1">
      <Fieldset locked={locked}><Sampling config={config} onChange={onChange} /></Fieldset>
      </AccordionContent>
      </AccordionItem>

      <AccordionItem value="reasoning">
      <AccordionTrigger className="text-sm">Reasoning</AccordionTrigger>
      <AccordionContent className="pt-1">
        <Fieldset locked={locked}><Reasoning config={config} onChange={onChange} /></Fieldset>
      </AccordionContent>
      </AccordionItem>

      <AccordionItem value="schema">
      <AccordionTrigger className="text-sm">Structured output</AccordionTrigger>
      <AccordionContent className="pt-1">
        <Fieldset locked={locked}><StructuredOutput config={config} onChange={onChange} /></Fieldset>
      </AccordionContent>
      </AccordionItem>

      <AccordionItem value="prompt">
      <AccordionTrigger className="text-sm">System &amp; tools</AccordionTrigger>
      <AccordionContent className="pt-1">
      <Fieldset locked={locked}>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="pg-system">System prompt</Label>
        <Textarea
          id="pg-system"
          rows={4}
          value={config.system}
          onChange={(e) => set("system", e.target.value)}
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="pg-tools">Tools</Label>
        <Textarea
          id="pg-tools"
          rows={4}
          placeholder='JSON array, e.g. [{"type":"function",…}]'
          value={config.toolsRaw}
          onChange={(e) => set("toolsRaw", e.target.value)}
          className="font-mono text-sm"
        />
        {toolsError && <p className="text-sm text-[hsl(var(--destructive))]">{toolsError}</p>}
      </div>
      </Fieldset>
      </AccordionContent>
      </AccordionItem>
      </Accordion>
    </div>
  )
}

/** A group of controls the lock reaches. Borderless and laid out as the plain
 *  column it replaces, so switching the lock on changes nothing but whether
 *  the controls answer. */
function Fieldset({ locked, children }: { locked: boolean; children: ReactNode }) {
  return (
    <fieldset disabled={locked} className="m-0 flex min-w-0 flex-col gap-4 border-0 p-0">
      {children}
    </fieldset>
  )
}
