import { Label, Textarea } from "darkraise-ui"
import { GatedField, retainedValueClass } from "./gated-field"
import { reasonFor } from "../dialect-support"
import { parseSchema } from "../lib/request"
import type { PlaygroundConfig } from "../config"

/**
 * A schema, never a switch.
 *
 * Two of the three edges honour structured output only when a schema is
 * present -- OpenAI drops a bare {"type":"json_object"}, and Gemini reads
 * responseSchema while ignoring responseMimeType. A JSON-mode toggle would
 * therefore be a control that did nothing on the dialect most operators use.
 */
export function StructuredOutput({
  config,
  onChange,
}: {
  config: PlaygroundConfig
  onChange: (next: PlaygroundConfig) => void
}) {
  const why = reasonFor(config.dialect, "schema")
  const { error } = parseSchema(config.schemaRaw)

  return (
    <GatedField reason={why}>
      <Label htmlFor="pg-schema">Response schema</Label>
      <Textarea
        id="pg-schema" rows={4}
        placeholder='JSON Schema, e.g. {"type":"object","properties":{…}}'
        disabled={why !== null}
        value={config.schemaRaw}
        onChange={(e) => onChange({ ...config, schemaRaw: e.target.value })}
        className={`font-mono text-sm ${retainedValueClass(config.schemaRaw)}`}
      />
      {error ? <p className="text-sm text-[hsl(var(--destructive))]">{error}</p> : null}
    </GatedField>
  )
}
