import type { StreamEvent } from '../types/agent'

export function parseStreamEvents(input: string, flush = false): { events: StreamEvent[]; rest: string } {
  const events: StreamEvent[] = []
  let index = 0

  while (index < input.length) {
    while (index < input.length && /\s/.test(input[index] || '')) {
      index += 1
    }
    if (index >= input.length) {
      return { events, rest: '' }
    }
    if (input[index] !== '{') {
      const nextObject = input.indexOf('{', index)
      if (nextObject < 0) {
        return { events, rest: flush ? input.slice(index) : '' }
      }
      index = nextObject
    }

    const end = findJSONEnd(input, index)
    if (end < 0) {
      return { events, rest: input.slice(index) }
    }
    events.push(JSON.parse(input.slice(index, end + 1)) as StreamEvent)
    index = end + 1
  }

  return { events, rest: '' }
}

function findJSONEnd(input: string, start: number) {
  let depth = 0
  let inString = false
  let escaped = false
  for (let i = start; i < input.length; i += 1) {
    const char = input[i]
    if (inString) {
      if (escaped) {
        escaped = false
      } else if (char === '\\') {
        escaped = true
      } else if (char === '"') {
        inString = false
      }
      continue
    }
    if (char === '"') {
      inString = true
    } else if (char === '{') {
      depth += 1
    } else if (char === '}') {
      depth -= 1
      if (depth === 0) {
        return i
      }
    }
  }
  return -1
}
