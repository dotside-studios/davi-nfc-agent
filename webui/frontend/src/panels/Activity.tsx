import type { LiveEvent, LogEntry } from '../types'
import type { TagLink } from '../useTags'
import { Live } from './Live'
import { Logs } from './Logs'

/**
 * Both streams side by side: tag events on the left, agent log on the right.
 * They are usually read together — a failed write and the line explaining it
 * arrive at the same moment — and a wide screen has room for both.
 */
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
