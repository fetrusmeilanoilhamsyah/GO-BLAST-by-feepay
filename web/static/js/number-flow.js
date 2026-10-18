/**
 * number-flow.js - Vanilla Web Component untuk Animasi Odometer / Angka Bergulir
 * Murni Native JavaScript & CSS transform (Zero Dependency, Aman CSP).
 */
(function() {
  'use strict';

  if (customElements.get('number-flow')) return;

  const styleTemplate = document.createElement('template');
  styleTemplate.innerHTML = `
    <style>
      :host {
        display: inline-flex;
        align-items: baseline;
        font-variant-numeric: tabular-nums;
        font-feature-settings: "tnum";
        line-height: 1;
        overflow: hidden;
        vertical-align: baseline;
      }
      .nf-char {
        display: inline-block;
        height: 1.15em;
        line-height: 1.15em;
        overflow: hidden;
        position: relative;
      }
      .nf-column {
        display: flex;
        flex-direction: column;
        transition: transform 0.65s cubic-bezier(0.16, 1, 0.3, 1);
        will-change: transform;
      }
      .nf-digit {
        height: 1.15em;
        display: flex;
        align-items: center;
        justify-content: center;
        text-align: center;
      }
      .nf-symbol {
        display: inline-block;
        height: 1.15em;
        line-height: 1.15em;
      }
    </style>
    <div id="container" style="display: inline-flex; align-items: baseline;"></div>
  `;

  class NumberFlowElement extends HTMLElement {
    static get observedAttributes() {
      return ['value', 'prefix', 'suffix'];
    }

    constructor() {
      super();
      this.attachShadow({ mode: 'open' });
      this.shadowRoot.appendChild(styleTemplate.content.cloneNode(true));
      this._container = this.shadowRoot.getElementById('container');
      this._currentChars = [];
    }

    connectedCallback() {
      this.render(this.getAttribute('value') || this.textContent.trim());
    }

    attributeChangedCallback(name, oldVal, newVal) {
      if (oldVal !== newVal && this.isConnected) {
        this.render(this.getAttribute('value') || '');
      }
    }

    get value() {
      return this.getAttribute('value') || '';
    }

    set value(val) {
      this.setAttribute('value', String(val));
    }

    render(valStr) {
      if (!valStr && valStr !== '0') {
        this._container.innerHTML = '';
        this._currentChars = [];
        return;
      }

      // Format string dengan prefix / suffix jika ada
      const prefix = this.getAttribute('prefix') || '';
      const suffix = this.getAttribute('suffix') || '';
      const fullText = prefix + valStr + suffix;
      const targetChars = fullText.split('');

      // Jika jumlah karakter berubah atau kontainer masih kosong, buat ulang kerangka DOM
      if (this._currentChars.length !== targetChars.length || this._container.children.length === 0) {
        this._container.innerHTML = '';
        this._currentChars = [];

        targetChars.forEach((ch, idx) => {
          if (/\d/.test(ch)) {
            const charBox = document.createElement('span');
            charBox.className = 'nf-char';
            
            const col = document.createElement('span');
            col.className = 'nf-column';
            // Buat urutan digit 0 sampai 9
            for (let d = 0; d <= 9; d++) {
              const dEl = document.createElement('span');
              dEl.className = 'nf-digit';
              dEl.textContent = String(d);
              col.appendChild(dEl);
            }
            charBox.appendChild(col);
            this._container.appendChild(charBox);

            const digitVal = parseInt(ch, 10);
            col.style.transform = `translateY(-${digitVal * 1.15}em)`;
            this._currentChars[idx] = { isDigit: true, col, val: digitVal };
          } else {
            const sym = document.createElement('span');
            sym.className = 'nf-symbol';
            sym.textContent = ch;
            this._container.appendChild(sym);
            this._currentChars[idx] = { isDigit: false, el: sym, val: ch };
          }
        });
        return;
      }

      // Jika jumlah karakter sama, hanya geser kolom digit yang nilainya berubah!
      targetChars.forEach((ch, idx) => {
        const item = this._currentChars[idx];
        if (!item) return;

        if (item.isDigit && /\d/.test(ch)) {
          const digitVal = parseInt(ch, 10);
          if (item.val !== digitVal) {
            item.val = digitVal;
            item.col.style.transform = `translateY(-${digitVal * 1.15}em)`;
          }
        } else if (!item.isDigit) {
          if (item.val !== ch) {
            item.val = ch;
            item.el.textContent = ch;
          }
        }
      });
    }
  }

  customElements.define('number-flow', NumberFlowElement);
})();
