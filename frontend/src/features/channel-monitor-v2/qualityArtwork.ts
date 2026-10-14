import DOMPurify from 'dompurify'

// Saved model output is never inserted into the application document.
// Sanitization removes navigation; sandbox and CSP independently block scripts and network access.
export function qualityArtworkDocument(html: string): string {
  const root = DOMPurify.sanitize(html, {
    WHOLE_DOCUMENT: true,
    RETURN_DOM: true,
    USE_PROFILES: { html: true, svg: true, svgFilters: true },
    FORBID_TAGS: ['script', 'iframe', 'frame', 'frameset', 'object', 'embed', 'link', 'meta', 'base', 'a', 'form', 'foreignObject'],
    FORBID_ATTR: ['srcdoc', 'action', 'formaction', 'target', 'ping'],
  })
  if (!(root instanceof HTMLElement)) throw new Error('Invalid quality artwork document')
  for (const element of root.querySelectorAll('*')) {
    for (const name of ['href', 'xlink:href', 'src', 'poster', 'background']) {
      const value = element.getAttribute(name)
      if (value && !value.startsWith('#')) element.removeAttribute(name)
    }
    element.removeAttribute('srcset')
  }
  const head = root.querySelector('head')
  if (!head) throw new Error('Invalid quality artwork document')
  const policy = document.createElement('meta')
  policy.setAttribute('http-equiv', 'Content-Security-Policy')
  policy.setAttribute('content', "default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; img-src 'none'; font-src 'none'; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'")
  head.prepend(policy)
  return `<!doctype html>${root.outerHTML}`
}
