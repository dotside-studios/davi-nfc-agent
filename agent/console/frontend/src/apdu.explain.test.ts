import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import { explain, parseHex } from './apdu'

/**
 * Drift guard for the APDU explainer.
 *
 * The console decodes a command in TypeScript for instant feedback, so `explain`
 * in apdu.ts is a hand-written mirror of the Go decoder (nfc.Explain). This test
 * asserts the mirror against the same shared fixture the Go side is held to
 * (nfc/testdata/apdu_explain_cases.json, generated from nfc.Explain and verified
 * by TestExplainContract). If the two decoders ever disagree, one of the two
 * tests goes red.
 *
 * When this test fails after a deliberate change: regenerate the fixture from Go
 * (UPDATE_APDU_FIXTURES=1 go test ./nfc/ -run TestExplainContract) and bring the
 * matching change across into apdu.ts so both sides produce it.
 */

interface Case {
  hex: string
  raw: boolean
  summary: string
  cls: string
  mutating: boolean
  recognized: boolean
  warnings: string[]
}

const fixtureURL = new URL('../../../../nfc/testdata/apdu_explain_cases.json', import.meta.url)
const cases = JSON.parse(readFileSync(fileURLToPath(fixtureURL), 'utf8')) as Case[]

function bytesOf(hex: string): Uint8Array {
  if (hex === '') return new Uint8Array(0)
  const b = parseHex(hex)
  if (!b) throw new Error(`fixture hex is not parseable: ${hex}`)
  return b
}

describe('explain matches the Go decoder contract', () => {
  it('reads a non-empty fixture', () => {
    expect(cases.length).toBeGreaterThan(0)
  })

  for (const c of cases) {
    const label = `${c.raw ? '[raw] ' : ''}${c.hex || '(empty)'} → ${c.cls}`
    it(label, () => {
      const e = explain(bytesOf(c.hex), c.raw)
      expect(e.summary).toBe(c.summary)
      expect(e.cls).toBe(c.cls)
      expect(e.mutating).toBe(c.mutating)
      expect(e.recognized).toBe(c.recognized)
      expect(e.warnings).toEqual(c.warnings)
    })
  }
})
