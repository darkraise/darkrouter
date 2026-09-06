import {
  Checkbox, Input, Label,
  PasswordInput, PasswordInputControl, PasswordInputField,
  Textarea, ToggleGroup, ToggleGroupItem,
} from "darkraise-ui"
import { PasswordToggle } from "../shell/password-toggle"

export type AccountDraft = {
  mode: "single" | "bulk"
  label: string
  secret: string
  bulk: string
  /** Narrows what the next discovery sweep imports for this provider. A
   *  provider setting rather than a property of the keys being added: against
   *  a provider that already exists the dialog seeds it from what that
   *  provider holds and writes it back when it changes. */
  freeModelsOnly: boolean
  /** Probe each key as it is added and keep only the ones that answer. */
  verifyKeys: boolean
  /** The operator's own account identifier, for a provider whose endpoint
   *  carries one -- Cloudflare Workers AI serves under /accounts/{id}/ai/v1.
   *  Empty for the rest of the catalogue. It rides on the credential rather
   *  than the provider because the account is the key's. */
  accountId: string
}

export const emptyAccounts: AccountDraft = {
  mode: "single",
  label: "",
  secret: "",
  bulk: "",
  freeModelsOnly: false,
  verifyKeys: true,
  accountId: "",
}

export type ParsedAccount = { label: string; secret: string; account_id?: string }

/** The one placeholder a shipped base URL may carry. Kept in step with the
 *  gateway's resolver, which refuses any other. */
const ACCOUNT_PLACEHOLDER = "{account_id}"

/** Whether this provider's endpoint cannot be reached by a key alone. Read
 *  from the base URL rather than a flag, because the URL is what actually
 *  needs the value and the two could not then disagree. */
export function needsAccount(baseURL?: string): boolean {
  return (baseURL ?? "").includes(ACCOUNT_PLACEHOLDER)
}

/** What the secret field asks for. Most providers hand out a key; a local CLI
 *  hands out a session document, and asking for an "API key" would send the
 *  operator looking for something their vendor does not issue. */
export type SecretField = {
  label: string
  placeholder: string
  /** A session document is JSON several lines long; a key is one short line. */
  multiline: boolean
  help?: string
}

const API_KEY_FIELD: SecretField = {
  label: "API key",
  placeholder: "sk-…",
  multiline: false,
}

/**
 * The secret field for one preset.
 *
 * Keyed on the preset rather than on the auth style, because the style says
 * how the credential is transmitted and this says what the operator has to go
 * and find — two different questions that happen to coincide for most of the
 * catalogue.
 */
export function secretFieldFor(presetID?: string): SecretField {
  if (presetID === "auggie") {
    return {
      label: "Augment session",
      placeholder: '{"accessToken":"…","tenantURL":"…"}',
      multiline: true,
      help:
        "Run `auggie login` on a machine with a browser, then paste the contents of " +
        "~/.augment/session.json. Leave this empty if you ran `auggie login` inside " +
        "the container instead — the CLI keeps its own session and needs nothing here.",
    }
  }
  return API_KEY_FIELD
}

/**
 * One account per non-empty line, in `name|key` form.
 *
 * The name is optional: a line with no pipe is all key, which is what a
 * column pasted out of a password manager looks like. A key containing a pipe
 * survives, because only the first one splits.
 *
 * Where the provider's endpoint carries an account the line carries one too,
 * as `account|key` or `name|account|key`. Each key belongs to its own account
 * -- a Cloudflare token is issued under one -- so a paste of five keys is five
 * accounts, and a single field applied to all of them would send four of the
 * five to the wrong address. A line naming no account is dropped: it cannot
 * address the provider, and creating it would leave a credential the gateway
 * refuses on every request.
 *
 * Duplicates are dropped by secret rather than by name: pasting a column
 * twice is a slip, and two credentials holding one key cool and fail as one
 * while presenting as two working accounts. Surrounding quotes and commas
 * come off because a paste out of a CSV or a JSON array brings them along.
 */
export function parseBulkAccounts(
  text: string,
  prefix = "key",
  needsAccount = false,
): ParsedAccount[] {
  const seen = new Set<string>()
  const out: ParsedAccount[] = []
  const clean = (v: string) => v.trim().replace(/,$/, "").replace(/^["']|["']$/g, "").trim()

  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim()
    if (line === "") continue

    const first = line.indexOf("|")
    let name = ""
    let account = ""
    let secret = ""

    if (!needsAccount) {
      name = first === -1 ? "" : clean(line.slice(0, first))
      secret = clean(first === -1 ? line : line.slice(first + 1))
    } else {
      if (first === -1) continue
      const second = line.indexOf("|", first + 1)
      if (second === -1) {
        account = clean(line.slice(0, first))
        secret = clean(line.slice(first + 1))
      } else {
        // Only the first two pipes split, so a key carrying one survives here
        // exactly as it does in the two-field form.
        name = clean(line.slice(0, first))
        account = clean(line.slice(first + 1, second))
        secret = clean(line.slice(second + 1))
      }
      if (account === "") continue
    }

    if (secret === "" || seen.has(secret)) continue
    seen.add(secret)
    const entry: ParsedAccount = { label: name || `${prefix}-${out.length + 1}`, secret }
    if (account !== "") entry.account_id = account
    out.push(entry)
  }
  return out
}

/** Enough to recognise a key in the preview, never enough to use one. */
export function maskSecret(secret: string): string {
  if (secret.length <= 8) return "•".repeat(secret.length)
  return `${secret.slice(0, 4)}${"•".repeat(6)}${secret.slice(-4)}`
}

/** What the draft would create, in the order it will be written. */
export function draftAccounts(draft: AccountDraft, needsAccount = false): ParsedAccount[] {
  if (draft.mode === "bulk") {
    // The account comes from each line rather than the field: bulk is where
    // the accounts differ, which is the whole reason the column exists.
    return parseBulkAccounts(draft.bulk, draft.label.trim() || "key", needsAccount)
  }
  const secret = draft.secret.trim()
  if (secret === "") return []
  const one: ParsedAccount = { label: draft.label.trim() || "default", secret }

  // Absent rather than empty for the rest of the catalogue: a provider that
  // does not need an account should not be sent one, or every provider starts
  // looking like one that does.
  const account = draft.accountId.trim()
  if (needsAccount && account !== "") one.account_id = account
  return [one]
}

/**
 * The account step, shared by the wizard and the provider detail page.
 *
 * Single and bulk are one segmented control rather than two forms: they
 * produce the same thing, and an operator with three keys should not have to
 * find a different screen from the one with one key.
 */
export function AccountFields({
  value,
  onChange,
  autoFocus,
  field = API_KEY_FIELD,
  needsAccount = false,
}: {
  value: AccountDraft
  onChange: (next: AccountDraft) => void
  autoFocus?: boolean
  /** What this provider's secret is called and looks like. */
  field?: SecretField
  /** Whether this provider's endpoint carries the operator's account, and so
   *  cannot be reached by a key alone. */
  needsAccount?: boolean
}) {
  const parsed = parseBulkAccounts(value.bulk, value.label.trim() || "key", needsAccount)
  return (
    <div className="flex flex-col gap-4">
      {/* Above the key: it belongs to the same account the key does, and the
          endpoint is unreachable without it, so it is not an afterthought.
          Single mode only -- in bulk each line carries its own, because that
          is where the accounts differ. */}
      {needsAccount && value.mode === "single" ? (
        <div className="flex flex-col gap-2">
          <Label htmlFor="account-id">Account ID</Label>
          <Input
            id="account-id"
            value={value.accountId}
            spellCheck={false}
            className="font-mono"
            onChange={(e) => onChange({ ...value, accountId: e.target.value })}
          />
          <p className="text-sm text-[hsl(var(--muted-foreground))]">
            This provider serves each account at its own address, so a key on
            its own cannot reach it. One account per credential.
          </p>
        </div>
      ) : null}
      <ToggleGroup
        type="single"
        value={value.mode}
        onValueChange={(mode) => {
          // An empty value comes back when the pressed item is pressed again;
          // a mode has to be one thing or the other, so that is ignored.
          if (mode === "single" || mode === "bulk") onChange({ ...value, mode })
        }}
        aria-label="How many credentials to add"
        className="w-fit rounded-[var(--radius)] border bg-[hsl(var(--muted))] p-0.5"
      >
        <ToggleGroupItem value="single">Single credential</ToggleGroupItem>
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
          <div className="flex min-w-0 flex-1 flex-col gap-1.5">
            <Label htmlFor="account-secret">{field.label}</Label>
            {field.multiline ? (
              // A session document does not fit a one-line password box, and
              // pasting one into an input that hides what arrived is how a
              // truncated paste goes unnoticed until the first request fails.
              <Textarea
                id="account-secret"
                rows={4}
                autoFocus={autoFocus}
                value={value.secret}
                onChange={(e) => onChange({ ...value, secret: e.target.value })}
                placeholder={field.placeholder}
                className="font-mono text-sm"
              />
            ) : (
              // Revealable, for the reason the multiline branch above is
              // not masked at all: a truncated paste is invisible in a field
              // that hides what arrived, and it surfaces as a failed request
              // rather than as a typo.
              <PasswordInput className="w-72">
                <PasswordInputControl>
                  <PasswordInputField
                    id="account-secret"
                    autoFocus={autoFocus}
                    value={value.secret}
                    onChange={(e) => onChange({ ...value, secret: e.target.value })}
                    placeholder={field.placeholder}
                  />
                  <PasswordToggle />
                </PasswordInputControl>
              </PasswordInput>
            )}
            {field.help && (
              <p className="max-w-prose text-sm text-[hsl(var(--legend))]">{field.help}</p>
            )}
          </div>
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="account-bulk">Credentials, one per line</Label>
            <Textarea
              id="account-bulk"
              rows={12}
              value={value.bulk}
              onChange={(e) => onChange({ ...value, bulk: e.target.value })}
              placeholder={
                needsAccount
                  ? "acct-a1b2|cf-aaa…\nwork|acct-c3d4|cf-bbb…"
                  : "work|sk-aaa…\nspare|sk-bbb…\nsk-ccc…"
              }
              className="font-mono text-sm"
            />
            <p className="text-sm text-[hsl(var(--legend))]">
              {needsAccount ? (
                <>
                  <span className="font-mono">account|key</span>, or{" "}
                  <span className="font-mono">name|account|key</span>. Each key is issued
                  under its own account, so every line names one; a line without one is
                  skipped.
                </>
              ) : (
                <>
                  <span className="font-mono">name|key</span>, or just the key on its own.
                </>
              )}
            </p>
          </div>
          <div className="flex flex-wrap items-end gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="account-bulk-label">Label prefix</Label>
              <Input
                id="account-bulk-label"
                value={value.label}
                onChange={(e) => onChange({ ...value, label: e.target.value })}
                placeholder="key"
                className="w-40"
              />
              <span className="text-sm text-[hsl(var(--legend))]">
                For the lines that name no credential.
              </span>
            </div>
            {/* The count and the masked heads confirm the paste landed the way
                it looked: a stray wrapped line shows up here as one account too
                many, before anything is sent. */}
            <p className="text-sm text-[hsl(var(--legend))]">
              {parsed.length === 0
                ? "Nothing to import yet"
                : `${parsed.length} ${parsed.length === 1 ? "credential" : "credentials"} · ${parsed
                    .slice(0, 3)
                    .map((a) =>
                      a.account_id
                        ? `${a.label} ${a.account_id} ${maskSecret(a.secret)}`
                        : `${a.label} ${maskSecret(a.secret)}`,
                    )
                    .join(", ")}${parsed.length > 3 ? " …" : ""}`}
            </p>
          </div>
        </div>
      )}

      <div className="flex flex-col gap-3">
        <div className="flex items-start gap-2">
          <Checkbox
            id="account-free-only"
            checked={value.freeModelsOnly}
            onCheckedChange={(next) =>
              onChange({ ...value, freeModelsOnly: next === true })
            }
          />
          <div className="flex flex-col">
            <Label htmlFor="account-free-only">Import free models only</Label>
            <span className="text-sm text-[hsl(var(--legend))]">
              A discovery sweep keeps a model the provider's own free tier documents,
              one priced at zero, or one tagged <span className="font-mono">:free</span>.
              A model nobody has priced and no free tier covers is not imported —
              unpriced is not free.
            </span>
          </div>
        </div>

        <div className="flex items-start gap-2">
          <Checkbox
            id="account-verify"
            checked={value.verifyKeys}
            onCheckedChange={(next) => onChange({ ...value, verifyKeys: next === true })}
          />
          <div className="flex flex-col">
            <Label htmlFor="account-verify">Check every key before keeping it</Label>
            <span className="text-sm text-[hsl(var(--legend))]">
              Each key is probed as it is added, and any the provider refuses is
              removed again. Slower, and the only way a bad key is caught here rather
              than by the first request that needed it.
            </span>
          </div>
        </div>
      </div>
    </div>
  )
}
