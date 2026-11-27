import { get, writable } from 'svelte/store'
import { parse } from 'smol-toml'
import scheme from '../../catppuccin-mocha-2.toml?raw'
import type { Colors } from './canvas/render'

const { colors: c, ansi } = parse(scheme) as unknown as {
  colors: Record<string,string>, ansi: { colors: string[] }
}
export const theme = writable({cursor:c.cursor,selection_background:c.selection_bg,selection_foreground:c.selection_fg,font_family:'IosevkaTerm Nerd Font Mono',font_size:19,padding:5,background:c.bg,foreground:c.fg,background_opacity:0.9,blur:true,palette:ansi.colors,active_border:c.accent,inactive_border:c.border,cursor_text:c.selection_fg,cursor_shape:'block'})
export const monoStack=(t:any)=>`"${t.font_family}", "JetBrains Mono", ui-monospace, "SF Mono", Menlo, monospace`
const withOpacity=(hex:string,opacity:number)=>{const v=hex.replace('#','');return `rgba(${parseInt(v.slice(0,2),16)},${parseInt(v.slice(2,4),16)},${parseInt(v.slice(4,6),16)},${opacity})`}
const palette=Object.fromEntries(['black','red','green','yellow','blue','magenta','cyan','white','brightBlack','brightRed','brightGreen','brightYellow','brightBlue','brightMagenta','brightCyan','brightWhite'].map((key,i)=>[key,ansi.colors[i]]))
export const terminalThemeFor=(t:any)=>({background:withOpacity(c.bg,t.background_opacity??0.9), foreground:c.fg, cursor:c.cursor,
  selectionBackground:c.selection_bg, selectionForeground:c.selection_fg,
  ...palette})
export const terminalTheme=terminalThemeFor(get(theme))
export function configureTerminal(prefs:{font_family:string;font_size:number;opacity:number;blur?:boolean}){
  const family=prefs.font_family==='Iosevka Mono'?'IosevkaTerm Nerd Font Mono':prefs.font_family
  theme.update(current=>({...current,font_family:family||'IosevkaTerm Nerd Font Mono',font_size:prefs.font_size||19,background_opacity:prefs.opacity??0.9,blur:prefs.blur??true}))
}
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
