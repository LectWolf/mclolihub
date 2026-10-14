import DOMPurify from 'dompurify'

// Saved model output is rendered in an isolated iframe. Keep inline HTML/CSS/SVG artwork intact,
// including SVG symbol reuse and native SMIL animation, while removing executable or navigable content.
export function qualityArtworkDocument(html: string): string {
  const root = DOMPurify.sanitize(html, {
    WHOLE_DOCUMENT: true,
    RETURN_DOM: true,
    USE_PROFILES: { html: true, svg: true, svgFilters: true },
    ADD_TAGS: ['animate', 'animatetransform', 'animatemotion', 'mpath', 'set', 'use'],
    ADD_ATTR: [
      'attributeName', 'attributeType', 'from', 'to', 'by', 'values', 'type', 'begin', 'dur', 'end',
      'repeatCount', 'repeatDur', 'calcMode', 'keyTimes', 'keySplines', 'keyPoints', 'path', 'rotate',
      'origin', 'href', 'xlink:href',
    ],
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
  const layout = document.createElement('style')
  layout.textContent = 'html,body{margin:0!important;width:100%!important;height:100%!important;overflow:hidden!important}body{overscroll-behavior:none!important}svg{display:block;max-width:100%;max-height:100%}'
  head.prepend(layout)
  const policy = document.createElement('meta')
  policy.setAttribute('http-equiv', 'Content-Security-Policy')
  policy.setAttribute('content', "default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; img-src 'none'; font-src 'none'; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'")
  head.prepend(policy)
  return `<!doctype html>${root.outerHTML}`
}
