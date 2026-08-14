import type { ControlState } from '../types'
import { Devices } from './Devices'
import { Origins } from './Origins'

/**
 * Who may connect: paired devices and their policy, and the browser allowlist.
 * One page because they are the two halves of the same question, and neither
 * fills a tab on its own.
 */
export function Access({ state }: { state: ControlState }) {
  return (
    <div className="cols wide">
      <Devices state={state} />
      <Origins state={state} />
    </div>
  )
}
