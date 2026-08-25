import type { LiveEvent, LogEntry } from '../types'
import type { TagLink } from '../useTags'
import { Live } from './Live'
import { Logs } from './Logs'

/** Tag events and the agent log side by side; they are usually read together. */
export function Activity({
  events,
  logs,
  tagLink,
  onClearEvents,
  onClearLogs,
}: {
  events: LiveEvent[]
  logs: LogEntry[]
  tagLink: TagLink
  onClearEvents: () => void
  onClearLogs: () => void
}) {
  return (
    <div className="split">
      <Live events={events} link={tagLink} onClear={onClearEvents} />
      <Logs logs={logs} onClear={onClearLogs} />
    </div>
  )
}
