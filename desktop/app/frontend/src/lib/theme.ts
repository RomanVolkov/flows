import { get, writable } from 'svelte/store'
import { parse } from 'smol-toml'
import scheme from '../../catppuccin-mocha-2.toml?raw'
import type { State } from './graph'

const { colors: c, ansi } = parse(scheme) as unknown as {
  colors: Record<string,string>, ansi: { colors: string[] }
}
const defaults = {cursor:c.cursor,selection_background:c.selection_bg,selection_foreground:c.selection_fg,font_family:'IosevkaTerm Nerd Font Mono',font_size:19,padding:5,background:c.bg,foreground:c.fg,background_opacity:0.9,blur:true,palette:ansi.colors,active_border:c.accent,inactive_border:c.border,cursor_text:c.selection_fg,cursor_shape:'block'}
type Theme = typeof defaults
export const theme = writable<Theme>(defaults)
export const monoStack=(t:Theme)=>`"${t.font_family}", "JetBrains Mono", ui-monospace, "SF Mono", Menlo, monospace`
const palette=Object.fromEntries(['black','red','green','yellow','blue','magenta','cyan','white','brightBlack','brightRed','brightGreen','brightYellow','brightBlue','brightMagenta','brightCyan','brightWhite'].map((key,i)=>[key,ansi.colors[i]]))
export const terminalThemeFor=(t:Theme)=>({background:rgba(c.bg,t.background_opacity??0.9), foreground:c.fg, cursor:c.cursor,
  selectionBackground:c.selection_bg, selectionForeground:c.selection_fg,
  ...palette})
export function configureTerminal(prefs:{font_family:string;font_size:number;opacity:number;blur?:boolean}){
  const family=prefs.font_family==='Iosevka Mono'?'IosevkaTerm Nerd Font Mono':prefs.font_family
  theme.update(themeValue=>({...themeValue,font_family:family||'IosevkaTerm Nerd Font Mono',font_size:prefs.font_size||19,background_opacity:prefs.opacity??0.9,blur:prefs.blur??true}))
  applyTheme()
}
const rgbOf = (hex: string): [number, number, number] => {
  const h = hex.replace('#', '')
  return [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16)) as [number, number, number]
}
const hexOf = (rgb: number[]) => '#' + rgb.map((c) => Math.round(c).toString(16).padStart(2, '0')).join('')

// mix is a + (b - a) * t, per channel.
export function mix(a: string, b: string, t: number): string {
  const x = rgbOf(a), y = rgbOf(b)
  return hexOf(x.map((c, i) => c + (y[i] - c) * t))
}

export function rgba(hex: string, alpha: number): string {
  const [r, g, b] = rgbOf(hex)
  return `rgba(${r}, ${g}, ${b}, ${alpha})`
}

// stateColors gives each node state a palette colour: blue running, teal watching, yellow
// blocked, green done, red failed, the dim tones for what waits or stopped.
export function stateColors(t: Theme): Record<State, string> {
  const p = t.palette
  return {
    pending: p[8], starting: p[12], running: p[4], watching: p[6],
    blocked: p[3], done: p[2], failed: p[1], stopped: mix(p[8], t.background, 0.4),
  }
}

// vars are the CSS custom properties of a theme.
export function vars(t: Theme): Record<string, string> {
  const bg = t.background, fg = t.foreground
  const v: Record<string, string> = {
    '--bg': rgba(bg, t.background_opacity),
    '--bg-solid': bg,
    '--elevated': rgba(mix(bg, fg, 0.035), Math.min(1, t.background_opacity + 0.04)),
    '--surface': mix(bg, fg, 0.06),
    '--hover': mix(bg, fg, 0.09),
    '--border': mix(bg, fg, 0.16),
    '--border-subtle': mix(bg, fg, 0.09),
    '--text': fg,
    '--text-2': t.palette[7] || mix(fg, bg, 0.3),
    '--text-3': t.palette[8] || mix(fg, bg, 0.55),
    '--accent': t.selection_background,
    '--accent-ink': t.selection_foreground,
    '--accent-glow': rgba(t.selection_background, 0.12),
    '--danger': t.palette[1],
    '--mono': monoStack(t),
    '--term-size': `${t.font_size}px`,
  }
  for (const [s, c] of Object.entries(stateColors(t))) v[`--state-${s}`] = c
  return v
}

export function applyTheme() {
  for (const [key,value] of Object.entries(vars(get(theme)))) document.documentElement.style.setProperty(key,value)
}
