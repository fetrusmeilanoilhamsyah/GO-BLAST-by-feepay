/**
 * sonner-toast.js - Minimal Stacked Toast ala Sonner (Vercel/Linear style)
 * Zero dependency, pure Vanilla JS & CSS Variables (Aman CSP).
 */
(function() {
  'use strict';

  if (window.toast) return;

  const style = document.createElement('style');
  style.textContent = `
    .sonner-container {
      position: fixed;
      bottom: 24px;
      right: 24px;
      z-index: 9999;
      display: flex;
      flex-direction: column-reverse;
      align-items: flex-end;
      pointer-events: none;
      gap: 10px;
    }
    @media (max-width: 600px) {
      .sonner-container {
        bottom: 16px;
        right: 16px;
        left: 16px;
        align-items: stretch;
      }
    }
    .sonner-toast {
      pointer-events: auto;
      background-color: var(--color-card);
      color: var(--color-text);
      border: 1px solid var(--color-border);
      border-radius: 8px;
      padding: 12px 16px;
      font-size: 13px;
      font-weight: 500;
      box-shadow: 0 4px 14px var(--color-shadow);
      display: flex;
      align-items: center;
      gap: 10px;
      min-width: 260px;
      max-width: 380px;
      transform: translateY(16px) scale(0.96);
      opacity: 0;
      transition: transform 0.25s cubic-bezier(0.16, 1, 0.3, 1), opacity 0.2s ease;
    }
    .sonner-toast.show {
      transform: translateY(0) scale(1);
      opacity: 1;
    }
    .sonner-toast.hide {
      transform: translateY(8px) scale(0.95);
      opacity: 0;
    }
    .sonner-icon {
      flex-shrink: 0;
      width: 16px;
      height: 16px;
      display: inline-flex;
      align-items: center;
      justify-content: center;
    }
    .sonner-icon svg {
      width: 100%;
      height: 100%;
    }
    .sonner-success .sonner-icon { color: var(--color-success); }
    .sonner-error .sonner-icon { color: var(--color-danger); }
    .sonner-info .sonner-icon { color: var(--color-primary); }
    .sonner-content {
      flex: 1;
      line-height: 1.4;
    }
    .sonner-close {
      background: none;
      border: none;
      color: var(--color-muted);
      cursor: pointer;
      padding: 2px;
      font-size: 14px;
      line-height: 1;
      opacity: 0.6;
    }
    .sonner-close:hover {
      opacity: 1;
      color: var(--color-text);
    }
  `;
  document.head.appendChild(style);

  let container = null;
  function getContainer() {
    if (!container || !document.body.contains(container)) {
      container = document.createElement('div');
      container.className = 'sonner-container';
      document.body.appendChild(container);
    }
    return container;
  }

  const icons = {
    success: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"></polyline></svg>`,
    error: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="8" x2="12" y2="12"></line><line x1="12" y1="16" x2="12.01" y2="16"></line></svg>`,
    info: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="16" x2="12" y2="12"></line><line x1="12" y1="8" x2="12.01" y2="8"></line></svg>`
  };

  function createToast(message, type = 'info', duration = 3500) {
    const parent = getContainer();
    const el = document.createElement('div');
    el.className = `sonner-toast sonner-${type}`;

    el.innerHTML = `
      <span class="sonner-icon">${icons[type] || icons.info}</span>
      <span class="sonner-content">${message}</span>
      <button type="button" class="sonner-close" aria-label="Tutup">&times;</button>
    `;

    const closeBtn = el.querySelector('.sonner-close');
    const dismiss = () => {
      el.classList.remove('show');
      el.classList.add('hide');
      setTimeout(() => {
        if (el.parentNode) el.parentNode.removeChild(el);
      }, 250);
    };

    closeBtn.addEventListener('click', dismiss);

    parent.appendChild(el);
    // Trigger animation
    requestAnimationFrame(() => {
      el.classList.add('show');
    });

    if (duration > 0) {
      setTimeout(dismiss, duration);
    }

    return { dismiss };
  }

  window.toast = {
    success: (msg, dur) => createToast(msg, 'success', dur),
    error: (msg, dur) => createToast(msg, 'error', dur),
    info: (msg, dur) => createToast(msg, 'info', dur)
  };
})();
