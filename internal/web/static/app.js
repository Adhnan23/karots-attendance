// Small progressive enhancement. Every page works without it except auto-print, the task-form field toggling and ticking tasks.

// <form data-confirm="..."> asks first; then buttons are disabled so a double-click can't submit twice.
document.addEventListener('submit', e => {
  const form = e.target
  if (form.dataset.confirm && !confirm(form.dataset.confirm)) return e.preventDefault()
  // <form data-ask="..."> asks for a typed answer (e.g. "yes") and sends it as "answer".
  if (form.dataset.ask) {
    const a = prompt(form.dataset.ask)
    if (a === null) return e.preventDefault()
    form.elements.answer.value = a
  }
  setTimeout(() => form.querySelectorAll('button').forEach(b => (b.disabled = true)))
})

document.addEventListener('click', e => {
  if (e.target.closest('[data-print]')) print()
})

// Task form: show only the fields used by the chosen repeat kind.
const kind = document.querySelector('select[name=kind]')
if (kind) {
  const sync = () =>
    document.querySelectorAll('[data-kind]').forEach(el => {
      el.hidden = !el.dataset.kind.split(' ').includes(kind.value)
    })
  kind.addEventListener('change', sync)
  sync()
}

// Task sheet: print straight away, then go back.
const back = document.body.dataset.autoprint
if (back) {
  addEventListener('afterprint', () => (location.href = back))
  setTimeout(print, 300)
}

// Live pages reload every N seconds unless the user is typing.
const every = Number(document.body.dataset.refresh)
if (every) {
  setInterval(() => {
    if (!document.activeElement || !document.activeElement.matches('input, select, textarea')) location.reload()
  }, every * 1000)
}
