// Turning key presses into what a terminal program reads (xterm conventions). Cmd combinations are
// left to the app (copy, paste); undefined means "send nothing".

export interface Key {
  key: string
  ctrlKey?: boolean
  altKey?: boolean
  shiftKey?: boolean
  metaKey?: boolean
}

const ESC = '\x1b'
const csiFinal: Record<string, string> = { ArrowUp: 'A', ArrowDown: 'B', ArrowRight: 'C', ArrowLeft: 'D', Home: 'H', End: 'F' }
const tilde: Record<string, number> = { Insert: 2, Delete: 3, PageUp: 5, PageDown: 6, F5: 15, F6: 17, F7: 18, F8: 19, F9: 20, F10: 21, F11: 23, F12: 24 }
const ss3: Record<string, string> = { F1: 'P', F2: 'Q', F3: 'R', F4: 'S' }

// encodeKey: appkey is the application cursor keys mode of the screen (arrows send ESC O x).
export function encodeKey(e: Key, appkey = false): string | undefined {
  if (e.metaKey) return undefined
  const mod = 1 + (e.shiftKey ? 1 : 0) + (e.altKey ? 2 : 0) + (e.ctrlKey ? 4 : 0)
  if (e.key in csiFinal) {
    const f = csiFinal[e.key]
    if (mod > 1) return `${ESC}[1;${mod}${f}`
    return appkey ? `${ESC}O${f}` : `${ESC}[${f}`
  }
  if (e.key in tilde) return mod > 1 ? `${ESC}[${tilde[e.key]};${mod}~` : `${ESC}[${tilde[e.key]}~`
  if (e.key in ss3) return mod > 1 ? `${ESC}[1;${mod}${ss3[e.key]}` : `${ESC}O${ss3[e.key]}`
  switch (e.key) {
    case 'Enter':
      return e.altKey ? ESC + '\r' : '\r'
    case 'Backspace':
      return e.altKey ? ESC + '\x7f' : e.ctrlKey ? '\x08' : '\x7f'
    case 'Tab':
      return e.shiftKey ? `${ESC}[Z` : '\t'
    case 'Escape':
      return ESC
  }
  if (e.key.length !== 1 && [...e.key].length !== 1) return undefined // Shift, Control, dead keys …
  let s = e.key
  if (e.ctrlKey) {
    const c = s.toLowerCase().charCodeAt(0)
    if (c >= 97 && c <= 122) s = String.fromCharCode(c - 96)
    else if (s === ' ' || s === '@') s = '\x00'
    else if (s === '[') s = ESC
    else if (s === '\\') s = '\x1c'
    else if (s === ']') s = '\x1d'
    else if (s === '^') s = '\x1e'
    else if (s === '_' || s === '-') s = '\x1f'
    else return undefined
  }
  return e.altKey ? ESC + s : s
}

// encodePaste wraps a paste for a program that asked for bracketed paste, and sends line ends as
// the enter key does.
export function encodePaste(text: string, bracketed: boolean): string {
  const t = text.replace(/\r\n|\n/g, '\r')
  return bracketed ? `${ESC}[200~${t.replaceAll(`${ESC}[201~`, '')}${ESC}[201~` : t
}
