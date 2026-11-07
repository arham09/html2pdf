const form = document.querySelector('#editor');
const button = document.querySelector('#render');
const status = document.querySelector('#status');
const preview = document.querySelector('#pdf');
const download = document.querySelector('#download');
let currentURL;
fetch('/healthz').then(r => r.json()).then(r => {
  document.querySelector('#runtime').textContent = `${r.os}/${r.arch} · ${r.go}`;
}).catch(() => { document.querySelector('#runtime').textContent = 'Runtime unavailable'; });
form.addEventListener('submit', async event => {
  event.preventDefault();
  button.disabled = true;
  status.textContent = 'Rendering…';
  try {
    const response = await fetch('/render', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ html: document.querySelector('#html').value })
    });
    if (!response.ok) {
      const problem = await response.json();
      throw new Error(problem.error?.message || `Request failed (${response.status})`);
    }
    const blob = await response.blob();
    const previousURL = currentURL;
    currentURL = URL.createObjectURL(blob);
    preview.src = currentURL;
    preview.hidden = false;
    document.querySelector('#placeholder').hidden = true;
    download.href = currentURL;
    download.hidden = false;
    if (previousURL) URL.revokeObjectURL(previousURL);
    status.textContent = `${response.headers.get('X-PDF-Pages')} page(s) · ${response.headers.get('X-PDF-Warnings')} warning(s) · ${Math.ceil(blob.size / 1024)} KiB`;
  } catch (error) { status.textContent = error.message; }
  finally { button.disabled = false; }
});
window.addEventListener('pagehide', () => { if (currentURL) URL.revokeObjectURL(currentURL); });
