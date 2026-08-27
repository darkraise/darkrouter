import { Tabs, TabsContent, TabsList, TabsTrigger } from "darkraise-ui/components/tabs"
import { Chat } from "./chat"
import { Compare } from "./compare"
import { AuxPanels, Count } from "./aux-panels"

// A thin composition root: each surface is a self-contained feature, so this
// file only wires the four together and holds none of their state.
export function PlaygroundScreen() {
  return (
    <Tabs defaultValue="chat">
      <TabsList className="mx-6 mt-4">
        <TabsTrigger value="chat">Chat</TabsTrigger>
        <TabsTrigger value="compare">Compare</TabsTrigger>
        <TabsTrigger value="auxiliary">Auxiliary</TabsTrigger>
        <TabsTrigger value="count">Count</TabsTrigger>
      </TabsList>
      <TabsContent value="chat">
        <Chat />
      </TabsContent>
      <TabsContent value="compare">
        <Compare />
      </TabsContent>
      <TabsContent value="auxiliary">
        <AuxPanels />
      </TabsContent>
      <TabsContent value="count">
        <Count />
      </TabsContent>
    </Tabs>
  )
}
