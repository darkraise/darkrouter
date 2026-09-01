import { api } from "../../../lib/api"
import { useApiMutation } from "../../../lib/mutations"
import { keys } from "../../../lib/queries"
import { mergeStoredConfig, toStoredConfig } from "../preset-config"
import { DIALECTS, type PlaygroundConfig } from "../config"
import type { TurnRoute } from "../message"
import type {
  PlaygroundConversation,
  PlaygroundMessage,
  PlaygroundStoredTurn,
} from "../../../lib/api-types"

/** 9router's rule, and it is a good one: long enough to tell two conversations
 *  apart, short enough for a 260px rail. */
const TITLE_MAX = 52

/**
 * A conversation's name, taken from the turn that started it.
 *
 * On a word boundary rather than at the character: a title cut mid-word reads
 * as a rendering fault, and the rail is a list of them.
 */
export function titleFromPrompt(prompt: string): string {
  const clean = prompt.trim().replace(/\s+/g, " ")
  if (clean === "") return "New chat"
  if (clean.length <= TITLE_MAX) return clean
  const cut = clean.slice(0, TITLE_MAX - 1)
  const boundary = cut.lastIndexOf(" ")
  // A single word longer than the limit has no boundary to cut on, so the
  // hard cut is the only honest answer.
  return (boundary > 0 ? cut.slice(0, boundary) : cut) + "…"
}

/**
 * A stored conversation, reconstituted into pane state.
 *
 * The same merge a preset gets, through the same function: section 8.3 says
 * the blob is the same shape, and a second implementation of the merge would
 * be a second set of rules about which stored fields are trustworthy.
 */
export function configOfConversation(c: PlaygroundConversation): PlaygroundConfig {
  // A row can be written by hand with curl, so the dialect column is a wire
  // value like any other: dialect-support.ts has no fallback case for one
  // outside the three it knows, so an unrecognized value crashes the pane's
  // render rather than degrading like the rest of the blob does.
  const dialect = DIALECTS.includes(c.dialect) ? c.dialect : "openai"
  return mergeStoredConfig(c.config, c.model, dialect)
}

/** The transcript, as the send loop holds it. */
export function messagesOfTurns(turns: PlaygroundStoredTurn[]): PlaygroundMessage[] {
  return turns.map((t) => ({ role: t.role, content: t.content }))
}

/**
 * What a reopened turn can still say about its routing.
 *
 * Only the trace link. The provider, the timings and the cost were read off a
 * trace at the time and are not stored, so filling them in from anywhere else
 * would print numbers nobody measured. A turn whose trace has been swept gets
 * no route at all, which is the state the transcript already renders for a
 * turn whose trace has not landed yet.
 */
export function routesOfTurns(turns: PlaygroundStoredTurn[]): Record<number, TurnRoute> {
  const out: Record<number, TurnRoute> = {}
  turns.forEach((turn, index) => {
    if (turn.request_id === "") return
    out[index] = {
      requestId: turn.request_id,
      provider: "",
      model: "",
      totalMs: null,
      tokensIn: null,
      tokensOut: null,
      reasoningTokens: 0,
      costMicros: null,
      failedOver: [],
      warnings: [],
    }
  })
  return out
}

/** What a write sends. model and dialect are columns; the rest is the blob. */
export function conversationBody(title: string, config: PlaygroundConfig) {
  return {
    title,
    dialect: config.dialect,
    model: config.model,
    config: toStoredConfig(config),
  }
}

/** No success toast on any of these: a conversation that saved itself is
 *  reported by the rail updating, and a toast per turn would be noise on every
 *  message the operator sends. A failure still toasts, through useApiMutation's
 *  single error path. */
export function useCreateConversation() {
  return useApiMutation<PlaygroundConversation, { title: string; config: PlaygroundConfig }>({
    mutationFn: (vars) =>
      api.post<PlaygroundConversation>(
        "/api/playground/conversations",
        conversationBody(vars.title, vars.config),
      ),
    // Shares the update hook's scope, so a create and the updates that follow
    // it are applied in the order they were made rather than raced.
    scope: { id: "playground-conversation" },
    invalidates: [keys.playgroundConversations],
  })
}

export function useAppendTurn() {
  return useApiMutation<
    { seq: number },
    { id: string; role: "user" | "assistant"; content: string; requestId: string }
  >({
    mutationFn: (vars) =>
      api.post<{ seq: number }>(`/api/playground/conversations/${vars.id}/messages`, {
        role: vars.role,
        content: vars.content,
        request_id: vars.requestId,
      }),
    // seq is assigned by the server in the order the appends arrive, and one
    // exchange sends two. Two exchanges whose saves overlap -- the second
    // model round trip outlasting the first pair of local writes -- would
    // otherwise interleave, and a transcript is only its seq order.
    scope: { id: "playground-turns" },
    invalidates: [keys.playgroundConversations],
  })
}

export function useUpdateConversation() {
  return useApiMutation<
    { id: string },
    { id: string; title: string; config: PlaygroundConfig }
  >({
    mutationFn: (vars) =>
      api.patch<{ id: string }>(
        `/api/playground/conversations/${vars.id}`,
        conversationBody(vars.title, vars.config),
      ),
    // A conversation is renamed and reconfigured from the same header, and the
    // whole row is sent each time. Two of these in flight together would leave
    // the stored row decided by which one the server finished last, so a model
    // name typed in two bursts could be stored as its shorter prefix.
    scope: { id: "playground-conversation" },
    invalidates: [keys.playgroundConversations],
  })
}

export function useDeleteConversation() {
  return useApiMutation<void, { id: string; title: string }>({
    mutationFn: (vars) => api.del<void>(`/api/playground/conversations/${vars.id}`),
    success: (_data, vars) => `Deleted ${vars.title}`,
    invalidates: [keys.playgroundConversations],
  })
}

export function usePurgeConversations() {
  return useApiMutation<{ deleted: number }, void>({
    mutationFn: () => api.del<{ deleted: number }>("/api/playground/conversations"),
    success: (data) =>
      data.deleted === 1 ? "Deleted 1 conversation" : `Deleted ${data.deleted} conversations`,
    invalidates: [keys.playgroundConversations],
  })
}
