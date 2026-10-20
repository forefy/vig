// Runs before first paint. Picks the stored theme, else the OS preference.
// External on purpose: the CSP forbids inline scripts.
(function () {
  var theme = null;
  try {
    var v = localStorage.getItem('theme');
    if (v === 'light' || v === 'dark') theme = v;
  } catch {
    /* no storage */
  }
  if (!theme) {
    var light = false;
    try {
      light = typeof matchMedia === 'function' && matchMedia('(prefers-color-scheme: light)').matches;
    } catch {
      /* no matchMedia */
    }
    theme = light ? 'light' : 'dark';
  }
  document.documentElement.classList.toggle('dark', theme === 'dark');
})();
// Reader settings chosen in the footer, applied before first paint as well.
(function () {
  var el = document.documentElement;
  try {
    var s = Number(localStorage.getItem('text-scale'));
    if (s && s !== 1 && s >= 0.5 && s <= 2) {
      el.style.zoom = String(s);
      el.style.setProperty('--text-scale', String(s));
    }
    if (localStorage.getItem('contrast') === 'high') el.classList.add('contrast');
    if (localStorage.getItem('motion') === 'reduce') el.classList.add('reduce-motion');
  } catch {
    /* no storage */
  }
})();
