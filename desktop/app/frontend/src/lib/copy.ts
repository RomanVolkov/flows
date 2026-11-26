const clipboard = (text: string) => window.go.main.App.Clipboard(text)

// use:copy — a click copies the element's text (or the given value) through the app: WKWebView
// refuses navigator.clipboard. A click that ends a text selection leaves it to the selection.
export function copy(el: HTMLElement, value?: string) {
  let text = value
  el.classList.add('copyable')
  el.title = 'Click to copy'
  let timer: ReturnType<typeof setTimeout> | undefined
  const click = async () => {
    if (window.getSelection()?.toString()) return
    const ok = await clipboard(text ?? el.textContent?.trim() ?? '').catch(() => false)
    el.dataset.copied = ok ? 'copied' : 'could not copy'
    clearTimeout(timer)
    timer = setTimeout(() => delete el.dataset.copied, 1200)
  }
  el.addEventListener('click', click)
  return {
    update(v?: string) {
      text = v
    },
    destroy() {
      clearTimeout(timer)
      el.removeEventListener('click', click)
    },
  }
}
