import { Input, Label, Textarea, ToggleGroup, ToggleGroupItem } from "darkraise-ui"

export type AccountDraft = {
  mode: "single" | "bulk"
  label: string
  secret: string
  bulk: string
}

export const emptyAccounts: AccountDraft = {
  mode: "single",
  label: "",
  secret: "",
  bulk: "",
}

/**
 * One secret per non-empty line, in the order they were pasted.
 *
 * Duplicates are dropped: pasting a column out of a spreadsheet twice is a
 * slip, and two credentials holding the same key would cool and fail as one
 * while presenting as two working accounts. Surrounding quotes and commas
 * come off because a paste out of a CSV or a JSON array brings them along.
 */
export function parseBulkSecrets(text: string): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const raw of text.split(/\r?\n/)) {
    const secret = raw.trim().replace(/,$/, "").replace(/^["']|["']$/g, "").trim()
    if (secret === "" || seen.has(secret)) continue
    seen.add(secret)
    out.push(secret)
  }
  return out
}

/** Enough to recognise a key in the preview, never enough to use one. */
export function maskSecret(secret: string): string {
  if (secret.length <= 8) return "•".repeat(secret.length)
  return `${secret.slice(0, 4)}${"•".repeat(6)}${secret.slice(-4)}`
}

/**
 * The account step, shared by the add-provider flow and the detail dialog.
 *
 * Single and bulk are one control rather than two forms: they produce the
 * same thing, and an operator with three keys should not have to find a
 * different screen from the one with one key.
 */
export function AccountFields({
  value,
  onChange,
  autoFocus,
}: {
  value: AccountDraft
  onChange: (next: AccountDraft) => void
  autoFocus?: boolean
}) {
  const parsed = parseBulkSecrets(value.bulk)
  return (
    <div className="flex flex-col gap-3">
      <ToggleGroup
        type="single"
        value={value.mode}
        onValueChange={(mode) => {
          // An empty value comes back when the pressed item is pressed again;
          // a mode has to be one thing or the other, so that is ignored.
          if (mode === "single" || mode === "bulk") onChange({ ...value, mode })
        }}
        aria-label="How many accounts to add"
      >
        <ToggleGroupItem value="single">Single account</ToggleGroupItem>
        <ToggleGroupItem value="bulk">Bulk import</ToggleGroupItem>
      </ToggleGroup>

      {value.mode === "single" ? (
        <div className="flex flex-wrap items-end gap-2">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="account-label">Label</Label>
            <Input
              id="account-label"
              value={value.label}
              onChange={(e) => onChange({ ...value, label: e.target.value })}
              placeholder="default"
              className="w-40"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="account-secret">API key</Label>
            <Input
              id="account-secret"
              type="password"
              autoFocus={autoFocus}
              value={value.secret}
              onChange={(e) => onChange({ ...value, secret: e.target.value })}
              placeholder="sk-…"
              className="w-72"
            />
          </div>
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="account-bulk">API keys, one per line</Label>
            <Textarea
              id="account-bulk"
              rows={5}
              value={value.bulk}
              onChange={(e) => onChange({ ...value, bulk: e.target.value })}
              placeholder={"sk-aaa…\nsk-bbb…\nsk-ccc…"}
              className="font-mono text-sm"
            />
          </div>
          <div className="flex flex-wrap items-end gap-2">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="account-bulk-label">Label prefix</Label>
              <Input
                id="account-bulk-label"
                value={value.label}
                onChange={(e) => onChange({ ...value, label: e.target.value })}
                placeholder="key"
                className="w-40"
              />
            </div>
            {/* The count and the masked heads are the confirmation that the
                paste landed the way it looked: a stray wrapped line shows up
                here as one key too many, before anything is sent. */}
            <p className="text-sm text-[hsl(var(--legend))]">
              {parsed.length === 0
                ? "Nothing to import yet"
                : `${parsed.length} ${parsed.length === 1 ? "key" : "keys"} · ${parsed
                    .slice(0, 3)
                    .map(maskSecret)
                    .join(", ")}${parsed.length > 3 ? " …" : ""}`}
            </p>
          </div>
        </div>
      )}
    </div>
  )
}
