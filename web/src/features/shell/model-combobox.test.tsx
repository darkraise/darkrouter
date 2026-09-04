import { useState } from "react"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"
import { ModelCombobox, filterCandidates, modelCandidates } from "./model-combobox"
import type { Model } from "../../lib/api-types"

const model = (name: string, providers: string[], surfaces = ["llm"]): Model => ({
  model: name, providers, surfaces, context_window: 0, max_output_tokens: 0,
  tools: false, vision: false, reasoning: false, inferred: false, state: "live",
  pricing: null, free_tier: null, merge_source: "discovered",
})

describe("modelCandidates", () => {
  it("offers both model forms, because both route", () => {
    // The bare name fans out across providers in priority order; the
    // qualified one pins to a single provider. A field wants either.
    expect(modelCandidates({ models: [model("llama", ["groq", "nebius"])] })).toEqual([
      "groq/llama", "llama", "nebius/llama",
    ])
  })

  it("names a model served by two providers once", () => {
    const out = modelCandidates({ models: [model("llama", ["groq"]), model("llama", ["nebius"])] })
    expect(out.filter((c) => c === "llama")).toHaveLength(1)
  })

  it("puts aliases first, because they resolve first", () => {
    // An exact alias match wins before provider/model or a bare name is
    // considered, and the list should read in that order.
    expect(modelCandidates({ models: [model("llama", ["groq"])], aliases: ["sonnet"] })).toEqual([
      "sonnet", "groq/llama", "llama",
    ])
  })

  it("offers no alias when none was given", () => {
    // Inside a chain the router expands targets through rules 2 and 3 only,
    // so an alias suggested there could never resolve.
    const out = modelCandidates({ models: [model("llama", ["groq"])] })
    expect(out).not.toContain("sonnet")
  })

  it("narrows to a surface when one is named", () => {
    // An embeddings field offering a chat model is offering a request the
    // executor will refuse.
    const models = [model("llama", ["groq"]), model("embed-3", ["groq"], ["embeddings"])]
    expect(modelCandidates({ models, surface: "embeddings" })).toEqual([
      "embed-3", "groq/embed-3",
    ])
  })

  it("keeps a model that serves the surface among others", () => {
    const models = [model("multi", ["groq"], ["llm", "embeddings"])]
    expect(modelCandidates({ models, surface: "embeddings" })).toContain("multi")
  })
})

describe("filterCandidates", () => {
  it("puts prefix matches ahead of matches from the middle", () => {
    // Typing "groq" means the provider, not every model with groq somewhere
    // in its name.
    expect(filterCandidates(["a-groq-thing", "groq/llama"], "groq")).toEqual([
      "groq/llama", "a-groq-thing",
    ])
  })

  it("ignores case, which no operator types consistently", () => {
    expect(filterCandidates(["groq/Llama-3.3"], "llama")).toEqual(["groq/Llama-3.3"])
  })

  it("caps the list, because a popover nobody scrolls to the end of is noise", () => {
    const many = Array.from({ length: 200 }, (_, i) => `groq/m-${i}`)
    expect(filterCandidates(many, "groq", 5)).toHaveLength(5)
  })

  it("shows the head of the list before anything is typed", () => {
    expect(filterCandidates(["a", "b", "c"], "  ", 2)).toEqual(["a", "b"])
  })
})

function Field({ candidates }: { candidates: string[] }) {
  const [value, setValue] = useState("")
  return (
    <ModelCombobox
      label="Model or alias"
      value={value}
      onChange={setValue}
      candidates={candidates}
    />
  )
}

describe("the field itself", () => {
  it("suggests what has been typed towards", async () => {
    render(<Field candidates={["sonnet", "groq/llama", "nebius/llama"]} />)
    const box = screen.getByLabelText("Model or alias")

    await userEvent.type(box, "groq")

    expect(await screen.findByText("groq/llama")).toBeInTheDocument()
    expect(screen.queryByText("sonnet")).not.toBeInTheDocument()
  })

  it("keeps text the catalogue has never heard of", async () => {
    // Discovery imports on a sweep. A field that discarded a name it did not
    // recognise would be unusable until the next one.
    render(<Field candidates={["groq/llama"]} />)
    const box = screen.getByLabelText("Model or alias")

    await userEvent.type(box, "brand-new-model")

    expect(box).toHaveValue("brand-new-model")
  })

  it("takes a suggestion when one is chosen", async () => {
    render(<Field candidates={["groq/llama"]} />)
    const box = screen.getByLabelText("Model or alias")

    await userEvent.type(box, "lla")
    await userEvent.click(await screen.findByText("groq/llama"))

    expect(box).toHaveValue("groq/llama")
  })
})

describe("browsing without typing", () => {
  it("opens its list on a click", async () => {
    // The complaint this exists for: a field that only reveals its list once
    // you have guessed the first character is a text box wearing a
    // combobox's markup, and these all sit in front of catalogues nobody has
    // memorised.
    render(
      <ModelCombobox
        label="Model or alias"
        value=""
        onChange={() => {}}
        candidates={["groq/llama", "groq/qwen"]}
      />,
    )
    await userEvent.click(screen.getByLabelText("Model or alias"))
    expect(await screen.findByText("groq/llama")).toBeInTheDocument()
    expect(screen.getByText("groq/qwen")).toBeInTheDocument()
  })

  it("offers a control that says it opens", async () => {
    render(
      <ModelCombobox
        label="Model or alias"
        value=""
        onChange={() => {}}
        candidates={["groq/llama"]}
      />,
    )
    await userEvent.click(
      screen.getByRole("button", { name: /show model or alias suggestions/i }),
    )
    expect(await screen.findByText("groq/llama")).toBeInTheDocument()
  })
})

describe("while the catalogue is still loading", () => {
  it("says it is loading rather than that nothing matches", async () => {
    // The two look identical from the component's side — an empty candidate
    // list — and only one of them is the operator's problem. Telling them
    // "nothing matches" while the request is in flight sends them off to
    // change what they typed.
    render(
      <ModelCombobox
        label="Model or alias"
        value=""
        onChange={() => {}}
        candidates={[]}
        loading
      />,
    )
    await userEvent.click(screen.getByLabelText("Model or alias"))

    expect(await screen.findByText(/loading the catalogue/i)).toBeInTheDocument()
    expect(screen.queryByText(/nothing in the catalogue matches/i)).not.toBeInTheDocument()
  })

  it("shows the wait on the closed field too", async () => {
    // Worth knowing before the popover is opened: an operator who clicks into
    // an empty list learns nothing about why it is empty.
    render(
      <ModelCombobox
        label="Model or alias"
        value=""
        onChange={() => {}}
        candidates={[]}
        loading
      />,
    )
    expect(
      screen.getByLabelText(/loading model or alias suggestions/i),
    ).toBeInTheDocument()
  })

  it("stops once there is something to suggest", async () => {
    // The catalogue polls. A spinner on every refetch would flicker over
    // suggestions that are already usable.
    render(
      <ModelCombobox
        label="Model or alias"
        value=""
        onChange={() => {}}
        candidates={["groq/llama"]}
        loading
      />,
    )
    expect(
      screen.queryByLabelText(/loading model or alias suggestions/i),
    ).not.toBeInTheDocument()

    await userEvent.click(screen.getByLabelText("Model or alias"))
    expect(await screen.findByText("groq/llama")).toBeInTheDocument()
  })

  it("says nothing matches once the catalogue has arrived", async () => {
    render(
      <ModelCombobox
        label="Model or alias"
        value=""
        onChange={() => {}}
        candidates={[]}
      />,
    )
    await userEvent.click(screen.getByLabelText("Model or alias"))

    expect(await screen.findByText(/nothing in the catalogue matches/i)).toBeInTheDocument()
    expect(screen.queryByText(/loading the catalogue/i)).not.toBeInTheDocument()
  })
})
