import { useState } from "react"
import { PageHeader } from "darkraise-ui/layout"
import { Badge, Button } from "darkraise-ui"
import { ColumnHeader, DataTable } from "darkraise-ui/data-table"
import { useModels } from "../../lib/queries"
import type { Model, Pricing } from "../../lib/api-types"
import { Ladder, type LadderRow, type PredictiveMark } from "../ladder/ladder"
import { OverrideEditor } from "./override-editor"

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

export function priceLabel(p: Pricing | null): string {
  // Unpriced is not free. An em-dash is the same claim the spend tile and
  // every cost cell already make.
  if (p === null) return "—"
  const dollars = (micros: number) => `$${(micros / 1_000_000).toFixed(2)}`
  return `${dollars(p.input_micros)} / ${dollars(p.output_micros)}`
}

export function priceBand(p: Pricing | null): string {
  if (p === null) return "unpriced"
  const perMTok = p.input_micros / 1_000_000
  if (perMTok < 1) return "under $1/MTok"
  if (perMTok <= 5) return "$1–$5/MTok"
  return "over $5/MTok"
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

// `darkraise-ui` bundles its own tanstack/react-table internally and does not
// re-export its column types, so the shape is pulled from the component's own
// signature rather than from a second, independently-versioned install of the
// same package — the two do not agree on what a ColumnDef looks like.
type Columns = Parameters<typeof DataTable<Row, unknown>>[0]["columns"]

function buildColumns(onEdit: (provider: string, model: string) => void): Columns {
  return [
    {
      accessorKey: "model",
      header: ({ column }) => <ColumnHeader column={column} title="Model" />,
      cell: ({ row }) => (
        <span className="font-mono text-xs">{row.original.model}</span>
      ),
    },
    {
      id: "serves",
      header: "Serves",
      cell: ({ row }) => (
        <Ladder mode="compressed" catalog rows={compressedRows(row.original)} />
      ),
    },
    {
      accessorKey: "context_window",
      header: ({ column }) => <ColumnHeader column={column} title="Context" />,
      cell: ({ row }) =>
        row.original.context_window ? (
          <span className="tabular-nums">
            {row.original.context_window.toLocaleString()}
          </span>
        ) : (
          "—"
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
      header: ({ column }) => <ColumnHeader column={column} title="Price" />,
      cell: ({ row }) => (
        <span className="tabular-nums">{priceLabel(row.original.pricing)}</span>
      ),
    },
    {
      accessorKey: "publisher",
      header: ({ column }) => <ColumnHeader column={column} title="Publisher" />,
      cell: ({ row }) => row.original.publisher || "—",
    },
    {
      accessorKey: "surface_list",
      header: ({ column }) => <ColumnHeader column={column} title="Surfaces" />,
      cell: ({ row }) => (
        <span className="font-mono text-xs">{row.original.surface_list || "—"}</span>
      ),
    },
    {
      // Grouped by the flattened "caps" string; rendered as the individual
      // badges the reader actually wants to scan.
      accessorKey: "caps",
      header: "Capabilities",
      cell: ({ row }) => (
        <div className="flex flex-wrap gap-1">
          {row.original.tools && <Badge variant="secondary">tools</Badge>}
          {row.original.vision && <Badge variant="secondary">vision</Badge>}
          {row.original.reasoning && <Badge variant="secondary">reasoning</Badge>}
          {/* Guessed rather than read. Master design §6.4 routes these
              with a warning, so the row has to say which they are. */}
          {row.original.inferred && <Badge variant="amber">inferred</Badge>}
        </div>
      ),
    },
    {
      accessorKey: "state",
      header: ({ column }) => <ColumnHeader column={column} title="State" />,
      cell: ({ row }) => <span className="font-mono text-xs">{row.original.state}</span>,
    },
    {
      accessorKey: "merge_source",
      header: ({ column }) => <ColumnHeader column={column} title="Source" />,
      cell: ({ row }) => (
        <Badge variant="outline" className="font-mono text-[10px]">
          {row.original.merge_source}
        </Badge>
      ),
    },
    {
      id: "actions",
      header: "",
      cell: ({ row }) => {
        // A model row folds every provider that serves it; an override is
        // per (provider, model), so the editor has to pick one. The catalog
        // order's first entry is the same provider the compressed ladder
        // draws first.
        const provider = row.original.providers[0]
        if (!provider) return null
        return (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onEdit(provider, row.original.model)}
          >
            Override
          </Button>
        )
      },
    },
  ]
}

export function ModelsScreen() {
  const catalog = useModels()
  const models = catalog.data?.models ?? []
  const [editing, setEditing] = useState<{ provider: string; model: string } | null>(null)

  return (
    <>
      <PageHeader
        title="Models"
        description="What it can route to, and which providers serve each one"
      />

      <DataTable
        data={models.map(facetRow)}
        columns={buildColumns((provider, model) => setEditing({ provider, model }))}
        facets={["surface_list", "state", "caps", "band", "merge_source"]}
        searchKey="model"
        searchPlaceholder="Search models"
        virtualize={{ rowHeight: 40, height: 640 }}
      />

      {editing && (
        <OverrideEditor
          provider={editing.provider}
          model={editing.model}
          onClose={() => setEditing(null)}
        />
      )}
    </>
  )
}
