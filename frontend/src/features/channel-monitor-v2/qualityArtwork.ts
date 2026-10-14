import DOMPurify from 'dompurify'

// Saved model output is never inserted into the application document.
// Sanitization removes navigation; sandbox and CSP independently block scripts and network access.
export function qualityArtworkDocument(html: string): string {
  const root = DOMPurify.sanitize(html, {
    WHOLE_DOCUMENT: true,
    RETURN_DOM: true,
    USE_PROFILES: { html: true, svg: true, svgFilters: true },
    ADD_TAGS: ['animate', 'animateTransform'],
    ADD_ATTR: ['attributeName', 'from', 'to', 'values', 'type', 'begin', 'dur', 'repeatCount', 'calcMode', 'keyTimes', 'keySplines'],
    FORBID_TAGS: ['script', 'iframe', 'frame', 'frameset', 'object', 'embed', 'link', 'meta', 'base', 'a', 'form', 'foreignObject'],
    FORBID_ATTR: ['srcdoc', 'action', 'formaction', 'target', 'ping'],
  })
  if (!(root instanceof HTMLElement)) throw new Error('Invalid quality artwork document')
  const animationAttributes = new Set(['transform', 'x', 'y', 'x1', 'x2', 'y1', 'y2', 'cx', 'cy', 'r', 'rx', 'ry', 'width', 'height', 'd', 'opacity', 'fill', 'fill-opacity', 'stroke', 'stroke-width', 'stroke-opacity', 'stroke-dashoffset'])
  for (const element of root.querySelectorAll('*')) {
    if (['animate', 'animatetransform'].includes(element.localName.toLowerCase())) {
      const attribute = (element.getAttribute('attributeName') ?? element.getAttribute('attributename') ?? '').toLowerCase()
      if (!animationAttributes.has(attribute)) { element.remove(); continue }
    }
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
