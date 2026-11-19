import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import './styles.css';
import './ward.css';

const root = document.getElementById('root');
if (!root) throw new Error('Не знайдено кореневий елемент застосунку.');

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);

// Service worker дає офлайн-роботу та показ push-сповіщень.
if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    void navigator.serviceWorker.register('/sw.js').catch(() => {
      // Без service worker застосунок працює, але без офлайн-режиму.
    });
  });
}
