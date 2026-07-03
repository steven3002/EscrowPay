import type { PocketState } from "@/lib/api";
import { stateLabel, stateTone } from "@/lib/format";
import { Badge } from "./ui";

// StateBadge renders a pocket's server state as a colored pill. The state shown
// is always the server's truth; the UI holds no lifecycle logic of its own.
export function StateBadge({ state }: { state: PocketState }) {
  return <Badge tone={stateTone(state)}>{stateLabel(state)}</Badge>;
}
