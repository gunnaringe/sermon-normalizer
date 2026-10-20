// Runs before the page is drawn (a plain script in <head>), so the chosen theme
// is there from the first paint: 'light' or 'dark' in localStorage; no choice
// (or 'auto') follows the device.
(function () {
  var theme = localStorage.getItem('theme');
  if (theme === 'light' || theme === 'dark') document.documentElement.dataset.theme = theme;
})();
