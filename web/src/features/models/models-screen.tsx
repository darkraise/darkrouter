import { useMemo, useState } from "react"
import { Link } from "@tanstack/react-router"
import { Badge, Button, Tooltip, TooltipContent, TooltipTrigger } from "darkraise-ui"
import { ModelCombobox } from "../shell/model-combobox"
import { CapabilityTriad, ScaleBar } from "../shell/measures"
import { ModelState, StatusMark } from "../shell/status-mark"
import { CircleCheck, TriangleAlert } from "lucide-react"
import { ColumnHeader, DataTable } from "darkraise-ui/data-table"
import { useModels } from "../../lib/queries"
import { useSearchFilters } from "../../lib/search-filters"
import type { FreeTier, Model, Pricing } from "../../lib/api-types"
import { pricePerMillion } from "../../lib/format"
import { Ladder, type LadderRow, type PredictiveMark } from "../ladder/ladder"
import { EmptyState, GhostRows, NoMatch } from "../shell/empty-state"
import { OverrideEditor } from "./override-editor"

const FIELDS = ["model", "provider"] as const

/**
 * The compressed ladder for one model: every provider that serves it, in
 * catalog order, with hollow marks.
 *
 * Nothing has been sent, so no mark may be filled. This is what the router
 * *would* consider, which is a different statement from what it did.
 */
export function compressedRows(m: Model): LadderRow<PredictiveMark>[] {
  return m.providers.map((provider, i) => ({
    rank: i + 1,
    mark: "skipped",
    target: `${provider}/${m.model}`,
  }))
}

/**
 * URL-backed prefilter, applied before the catalog reaches DataTable.
 *
 * DataTable's own search/facets narrow whatever data it is handed, but that
 * state is private to the component — nothing controls it and nothing reads
 * it back out. A reload or a pasted link would silently drop the filter, so
 * "which models am I even looking at" has to live in the URL instead, the
 * same way Requests keeps FilterSelect in the URL and layers its own facets
 * on top of the already-filtered page.
 */
export function matches(m: Model, filters: Record<string, string>): boolean {
  const model = filters.model?.toLowerCase() ?? ""
  const provider = filters.provider?.toLowerCase() ?? ""
  if (model && !m.model.toLowerCase().includes(model)) return false
  if (provider && !m.providers.some((p) => p.toLowerCase().includes(provider)))
    return false
  return true
}

/** The scale the context bar is drawn on: the smallest window the catalogue
 *  carries, and the largest. */
const CONTEXT_FLOOR = 4_000
const CONTEXT_CEILING = 2_000_000

/** Tokens as thousands, because 131072 is a number nobody holds in their head
 *  and 131k is the one on the vendor's own page. */
export function tokenLabel(tokens: number): string {
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(1)}M`
  if (tokens >= 1_000) return `${Math.round(tokens / 1_000)}k`
  return String(tokens)
}

/**
 * What a free tier actually grants, as one line: `free · ~24M tokens/day`.
 *
 * A zero token count is uncapped or unquantified, never "no allowance", so it
 * falls back to a bare `free` — `~0 tokens/day` would be a lie about a tier
 * that in fact has no ceiling. `discontinued` is not a free tier any more.
 */
export function freeLabel(t: FreeTier | null): string | null {
  if (!t || t.free_type === "" || t.free_type === "discontinued") return null
  const [tokens, period] =
    t.free_type === "recurring-daily"
      ? ([t.monthly_tokens, "day"] as const)
      : t.free_type === "recurring-monthly"
        ? ([t.monthly_tokens, "month"] as const)
        : t.free_type === "one-time-initial" || t.free_type === "recurring-credit"
          ? ([t.credit_tokens, "once"] as const)
          : ([0, ""] as const)
  if (tokens <= 0) return "free"
  const figure = tokenLabel(tokens).replace(".0", "")
  return `free · ~${figure} tokens${period === "once" ? " once" : `/${period}`}`
}

/** Upstream grades a free tier ok, caution, ambiguous, avoid or unknown.
 *  Only "avoid" marks access the vendor has not sanctioned, which the router
 *  refuses to use until the operator opts the provider in. */
export function tierWarning(t: FreeTier | null): string | null {
  if (!t || t.tos !== "avoid") return null
  return "free tier not sanctioned by the vendor — allow it on the provider before the router will use it"
}

export function priceBand(p: Pricing | null): string {
  if (p === null) return "unpriced"
  const perMTok = p.input_micros / 1_000_000
  if (perMTok < 1) return "under $1/MTok"
  if (perMTok <= 5) return "$1–$5/MTok"
  return "over $5/MTok"
}

/**
 * Whether a price is worth flagging.
 *
 * Only the two ends of the confidence scale get a mark. `indexed` and
 * `declared` are left bare: models.dev covers most of the catalogue, so
 * badging every indexed price would put a mark on nearly every row, and a
 * mark on everything is read as nothing.
 */
export function priceMarker(p: Pricing | null): "verified" | "caution" | null {
  if (p === null) return null
  if (p.price_grade === "measured") return "verified"
  if (p.price_grade === "guessed") return "caution"
  return null
}

/** The scalar shape DataTable's facets need. `surfaces` and price are arrays
 *  and objects respectively — a facet filters on exact values, and either one
 *  would render as one distinct entry per row rather than as a real group. */
export type Row = Model & { surface_list: string; caps: string; band: string }

export function facetRow(m: Model): Row {
  const caps = [
    m.tools && "tools",
    m.vision && "vision",
    m.reasoning && "reasoning",
  ].filter(Boolean) as string[]
  return {
    ...m,
    surface_list: m.surfaces.join(", "),
    // "none" rather than blank: an empty facet value groups every
    // capability-less model under a label that reads as a broken facet.
    caps: caps.length > 0 ? caps.join(", ") : "none",
    band: priceBand(m.pricing),
  }
}

/**
 * Who serves the model, folded to one chip.
 *
 * The full compressed ladder sat in every row and was what pushed Publisher,
 * Surfaces, State and Source off the right edge of a 1440px window. The
 * first provider — the one the router would arrive at first — is the chip;
 * the rest are a count, and the ladder opens under it on request.
 */
function ServesCell({ row }: { row: Row }) {
  const [open, setOpen] = useState(false)
  const first = row.providers[0]
  if (first === undefined) return <span className="text-[hsl(var(--legend))]">—</span>
  const rest = row.providers.length - 1
  return (
    <div className="flex flex-col gap-1">
      <span className="flex items-center gap-1.5 whitespace-nowrap">
        <Tooltip>
          <TooltipTrigger asChild>
            <span tabIndex={0} className="font-mono text-sm">
              {first}/{row.model}
            </span>
          </TooltipTrigger>
          <TooltipContent>
            <span className="flex flex-col font-mono text-sm">
              {row.providers.map((p) => (
                <span key={p}>
                  {p}/{row.model}
                </span>
              ))}
            </span>
          </TooltipContent>
        </Tooltip>
        {rest > 0 && (
          <Button
            size="sm"
            variant="ghost"
            aria-expanded={open}
            aria-label={`${open ? "Hide" : "Show"} the ${row.providers.length} providers serving ${row.model} (${rest} more)`}
            onClick={() => setOpen((o) => !o)}
          >
            {open ? "less" : `+${rest} more`}
          </Button>
        )}
      </span>
      {open && <Ladder mode="compressed" catalog rows={compressedRows(row)} />}
    </div>
  )
}

function ModelCell({ row }: { row: Model }) {
  const warning = tierWarning(row.free_tier)
  return (
    <span className="flex min-w-[16rem] flex-col">
      <span className="font-mono text-sm">{row.model}</span>
      {warning && (
        <span className="flex items-start gap-1.5 text-sm font-medium text-[hsl(var(--warning))]">
          <TriangleAlert className="mt-0.5 size-[var(--icon-size)] shrink-0" aria-hidden />
          {warning}
        </span>
      )}
    </span>
  )
}

// `darkraise-ui` bundles its own tanstack/react-table internally and does not
// re-export its column types, so the shape is pulled from the component's own
// signature rather than from a second, independently-versioned install of the
// same package — the two do not agree on what a ColumnDef looks like.
type Columns = Parameters<typeof DataTable<Row, unknown>>[0]["columns"]

// The facet columns take string headers rather than sortable ones: a facet
// and the column menu are both labelled from the header when it is a string
// and from the accessor key otherwise, and "surface_list" is not a label.
function buildColumns(onEdit: (providers: string[], model: string) => void): Columns {
  return [
    {
      accessorKey: "model",
      header: ({ column }) => <ColumnHeader column={column} title="Model" />,
      cell: ({ row }) => <ModelCell row={row.original} />,
    },
    {
      id: "serves",
      header: "Serves",
      cell: ({ row }) => <ServesCell row={row.original} />,
    },
    {
      accessorKey: "context_window",
      header: ({ column }) => <ColumnHeader column={column} title="Context" />,
      // Log-scaled: the catalogue runs from 4k to 2M, and on a linear scale
      // every model below a hundred thousand would share the same empty bar.
      cell: ({ row }) => (
        <ScaleBar
          value={row.original.context_window || null}
          min={CONTEXT_FLOOR}
          max={CONTEXT_CEILING}
          label={row.original.context_window ? tokenLabel(row.original.context_window) : "—"}
          title="Bar is log-scaled, 4k to 2M tokens"
        />
      ),
    },
    {
      accessorKey: "max_output_tokens",
      header: ({ column }) => <ColumnHeader column={column} title="Max output" />,
      cell: ({ row }) =>
        row.original.max_output_tokens ? (
          <span className="tabular-nums">
            {row.original.max_output_tokens.toLocaleString()}
          </span>
        ) : (
          "—"
        ),
    },
    {
      // Grouped by band rather than by exact price: a facet filters on exact
      // values, and no two models share a price down to the micro-dollar.
      accessorKey: "band",
      header: "Band",
      cell: ({ row }) => {
        const marker = priceMarker(row.original.pricing)
        const free = freeLabel(row.original.free_tier)
        return (
          <span className="flex flex-col">
            <span className="flex items-center gap-1.5 whitespace-nowrap tabular-nums">
              {row.original.pricing
                ? `${pricePerMillion(row.original.pricing.input_micros)} / ${pricePerMillion(row.original.pricing.output_micros)}`
                : "—"}
              {marker === "verified" && (
                <StatusMark icon={CircleCheck} tone="good" label="Price quoted by the provider" />
              )}
              {marker === "caution" && (
                <StatusMark
                  icon={TriangleAlert}
                  tone="warning"
                  label="No published price; this is an estimate"
                />
              )}
            </span>
            {free && (
              <span className="whitespace-nowrap text-sm text-[hsl(var(--legend))]">{free}</span>
            )}
          </span>
        )
      },
    },
    {
      accessorKey: "publisher",
      header: ({ column }) => <ColumnHeader column={column} title="Publisher" />,
      cell: ({ row }) => row.original.publisher || "—",
    },
    {
      accessorKey: "surface_list",
      header: "Surfaces",
      cell: ({ row }) => (
        <span className="font-mono text-sm">{row.original.surface_list || "—"}</span>
      ),
    },
    {
      // Grouped by the flattened "caps" string; rendered as the individual
      // badges the reader actually wants to scan.
      accessorKey: "caps",
      header: "Capabilities",
      cell: ({ row }) => (
        <div className="flex flex-wrap items-center gap-1.5">
          <CapabilityTriad
            tools={row.original.tools}
            vision={row.original.vision}
            reasoning={row.original.reasoning}
          />
          {/* Guessed rather than read. Master design §6.4 routes these
              with a warning, so the row has to say which they are — and it
              says it in words, because the triad beside it is about what the
              model does, not about how well the catalogue knows it. */}
          {row.original.inferred && <Badge variant="amber">inferred</Badge>}
        </div>
      ),
    },
    {
      accessorKey: "state",
      header: "State",
      cell: ({ row }) => <ModelState state={row.original.state} />,
    },
    {
      accessorKey: "merge_source",
      header: "Source",
      cell: ({ row }) => (
        <Badge variant="outline" className="font-mono text-sm">
          {row.original.merge_source}
        </Badge>
      ),
    },
    {
      id: "actions",
      header: "",
      // Not data, so not something to hide: the column-visibility menu labels
      // each entry with its header, and an empty header sits in that list as a
      // checkbox with no name. The same reason requests-columns.tsx pins its
      // own actions column.
      enableHiding: false,
      cell: ({ row }) => {
        // A model row folds every provider that serves it; an override is
        // per (provider, model), so the editor opens on the first — the one
        // the chip beside it names — and offers the rest.
        if (row.original.providers.length === 0) return null
        return (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onEdit(row.original.providers, row.original.model)}
          >
            Override
          </Button>
        )
      },
    },
  ]
}

export function ModelsScreen() {
  const [filters, setFilter, clear] = useSearchFilters(FIELDS)
  const catalog = useModels()
  // Straight from the catalogue this screen is already showing, so the
  // suggestions cannot disagree with the rows beneath them.
  const modelNames = useMemo(
    () => [...new Set((catalog.data?.models ?? []).map((m) => m.model))].sort(),
    [catalog.data],
  )
  const providerNames = useMemo(
    () => [...new Set((catalog.data?.models ?? []).flatMap((m) => m.providers))].sort(),
    [catalog.data],
  )
  const models = useMemo(
    () => (catalog.data?.models ?? []).filter((m) => matches(m, filters)),
    [catalog.data, filters],
  )
  // Both memoised: DataTable rebuilds its table model when either changes
  // identity, and the catalogue polls every thirty seconds.
  const rows = useMemo(() => models.map(facetRow), [models])
  const [editing, setEditing] = useState<{ providers: string[]; model: string } | null>(null)
  const columns = useMemo(
    () => buildColumns((providers, model) => setEditing({ providers, model })),
    [],
  )
  const filtered = Object.values(filters).some((v) => v !== "")

  return (
    <>
      {/* Suggesting, not constraining: both stay substring filters, so a
          partial name still narrows the table — but the catalogue is right
          here, and making an operator remember an exact model id to filter by
          one was a plain box in front of data the screen had already loaded. */}
      <div className="mb-4 flex flex-wrap gap-2">
        <ModelCombobox
          label="Filter by model"
          placeholder="Model"
          value={filters.model}
          onChange={(v) => setFilter("model", v)}
          candidates={modelNames}
          loading={catalog.isPending}
          emptyText="No model in the catalogue matches."
          className="w-56"
        />
        <ModelCombobox
          label="Filter by provider"
          placeholder="Provider"
          value={filters.provider}
          onChange={(v) => setFilter("provider", v)}
          candidates={providerNames}
          loading={catalog.isPending}
          emptyText="No provider serves a model matching that."
          className="w-56"
        />
      </div>

      {/* Paged rather than windowed: a row grows when its ladder is opened,
          and a fixed-height window cannot hold a row that changes height. */}
      <DataTable
        data={rows}
        columns={columns}
        facets={["surface_list", "state", "caps", "band", "merge_source"]}
        searchKey="model"
        searchPlaceholder="Search models"
        isLoading={catalog.isPending}
      />

      {models.length === 0 && (
        <div className="mt-4">
          {filtered ? (
            <NoMatch what="models" onClear={clear} />
          ) : (
            <EmptyState
              title="Discovery fills this catalogue in"
              hint="A sweep asks each provider what it serves, using one of that provider's own keys — so a provider needs an account before it can answer."
              action={
                <Button asChild size="sm">
                  <Link to="/providers">Add a provider credential</Link>
                </Button>
              }
              preview={<GhostRows />}
            />
          )}
        </div>
      )}

      {editing && (
        <OverrideEditor
          providers={editing.providers}
          model={editing.model}
          onClose={() => setEditing(null)}
        />
      )}
    </>
  )
}
