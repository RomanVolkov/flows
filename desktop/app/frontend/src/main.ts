import { applyTheme } from "./lib/theme"
applyTheme()
import './style.css'
import { mount } from 'svelte'
import App from './App.svelte'

// Canvas text metrics must be measured with the bundled face on first paint.
const app = document.fonts.load('19px "IosevkaTerm Nerd Font Mono"')
  .catch(() => [])
  .then(() => mount(App, { target: document.getElementById('app')! }))

export default app
