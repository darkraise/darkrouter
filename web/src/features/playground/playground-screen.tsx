import { Chat } from "./chat"

// A thin composition root: the chat surface is a self-contained feature so a
// later tab (compare mode, aux panels, token counting) can sit beside it here
// without reworking how the transcript itself streams and seeds.
export function PlaygroundScreen() {
  return <Chat />
}
