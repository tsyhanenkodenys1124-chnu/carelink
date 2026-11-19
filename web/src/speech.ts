// Озвучення тексту на екрані підопічного.

/** speak читає текст українською з указаною швидкістю. */
export function speak(text: string, rate = 1): void {
  if (!('speechSynthesis' in window) || !text) return;
  try {
    window.speechSynthesis.cancel();
    const utterance = new SpeechSynthesisUtterance(text);
    utterance.lang = 'uk-UA';
    utterance.rate = Math.min(Math.max(rate, 0.5), 1.5);
    const ukrainian = window.speechSynthesis.getVoices().find((voice) => voice.lang.startsWith('uk'));
    if (ukrainian) utterance.voice = ukrainian;
    window.speechSynthesis.speak(utterance);
  } catch {
    // Якщо синтез недоступний, екран просто лишається текстовим.
  }
}

/** stopSpeaking зупиняє озвучення. */
export function stopSpeaking(): void {
  if ('speechSynthesis' in window) window.speechSynthesis.cancel();
}

/** speechAvailable повідомляє, чи вміє браузер озвучувати текст. */
export function speechAvailable(): boolean {
  return 'speechSynthesis' in window;
}
