import { writable } from 'svelte/store'
import { parse } from 'smol-toml'
import scheme from '../../catppuccin-mocha-2.toml?raw'
import type { Colors } from './canvas/render'

const { colors: c, ansi } = parse(scheme) as unknown as {
  colors: Record<string,string>, ansi: { colors: string[] }
}
export const theme = writable({cursor:c.cursor,selection_background:c.selection_bg,selection_foreground:c.selection_fg,font_family:'IosevkaTerm Nerd Font Mono',font_size:13,padding:5,background:c.bg,foreground:c.fg,background_opacity:1,palette:ansi.colors,active_border:c.accent,inactive_border:c.border,cursor_text:c.selection_fg,cursor_shape:'block'})
export const monoStack=(t:any)=>`"${t.font_family}", ui-monospace, monospace`
export const terminalTheme = {background:c.bg, foreground:c.fg, cursor:c.cursor,
  selectionBackground:c.selection_bg, selectionForeground:c.selection_fg,
  ...Object.fromEntries(['black','red','green','yellow','blue','magenta','cyan','white','brightBlack','brightRed','brightGreen','brightYellow','brightBlue','brightMagenta','brightCyan','brightWhite'].map((key,i)=>[key,ansi.colors[i]]))}
export const colors: Colors = {ink:c.fg, dim:c.fg_muted, selected:c.selection_bg,
  font:'"Iosevka Mono", Menlo, monospace', attention:c.accent_alt, bg:c.bg,
  states:{pending:c.overlay, starting:c.accent_info,running:c.blue,watching:ansi.colors[6],
    blocked:c.warn,done:ansi.colors[2],failed:ansi.colors[1],stopped:c.fg_muted}}
export function applyTheme() {
  const vars = {bg:c.bg,'bg-solid':c.bg,elevated:c.bg_alt,surface:c.bg_deep,hover:c.surface,
    border:c.border,'border-subtle':c.surface,text:c.fg,'text-2':c.fg_dim,'text-3':c.fg_muted,
    accent:c.accent,'accent-ink':c.selection_fg,'accent-glow':'color-mix(in srgb, '+c.accent+' 12%, transparent)',
    danger:ansi.colors[1],mono:colors.font}
  for (const [key,val] of Object.entries(vars)) document.documentElement.style.setProperty('--'+key,val)
}
