import {
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  Textarea,
} from "darkraise-ui"
import { ModelCombobox, useModelCandidates } from "../shell/model-combobox"
import { DIALECTS, type PlaygroundConfig } from "./config"
import { parseTools } from "./chat"
import type { PlaygroundDialect } from "../../lib/api-types"

/**
 * The request, beside the surfaces that send it.
 *
 * A column rather than a row across the top: these are seven controls that are
 * set once and read many times, and a chat transcript needs the width more
 * than they do.
 */
export function ConfigPane({
  config,
  onChange,
}: {
  config: PlaygroundConfig
  onChange: (next: PlaygroundConfig) => void
}) {
  const { candidates, loading } = useModelCandidates()
  const set = <K extends keyof PlaygroundConfig>(key: K, value: PlaygroundConfig[K]) =>
    onChange({ ...config, [key]: value })
  const toolsError = parseTools(config.toolsRaw).error

  return (
    <aside className="flex w-full shrink-0 flex-col gap-4 border-l p-4 lg:w-80">
      <h2 className="text-sm font-medium">Request</h2>

      <ModelCombobox
        label="Model or alias"
        value={config.model}
        onChange={(model) => set("model", model)}
        candidates={candidates}
        loading={loading}
      />

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

      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1.5">
          <Label htmlFor="pg-temp">Temperature</Label>
          <Input
            id="pg-temp"
            type="number"
            placeholder="default"
            value={config.temperature}
            onChange={(e) => set("temperature", e.target.value)}
          />
        </div>
        <div className="flex flex-1 flex-col gap-1.5">
          <Label htmlFor="pg-max">Max tokens</Label>
          <Input
            id="pg-max"
            type="number"
            placeholder="default"
            value={config.maxTokens}
            onChange={(e) => set("maxTokens", e.target.value)}
          />
        </div>
      </div>

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

      <label className="flex items-center gap-2 text-sm">
        <Switch checked={config.stream} onCheckedChange={(next) => set("stream", next)} />
        Stream the reply
      </label>
    </aside>
  )
}
