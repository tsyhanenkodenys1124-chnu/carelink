// Перевірка екрана підопічного за портретами користувачів.
//
// Для кожного портрета застосунок налаштовується анкетою функціональних потреб,
// після чого перевіряється не стан у базі, а те, що насправді бачить людина:
// розмір тексту, палітра, розмір кнопок, наявність потрібних і відсутність зайвих
// елементів, контраст та відсутність горизонтального прокручування.
//
// Портрети описані в docs/doslidzhennya.md і повторені в
// backend/internal/api/portraits_test.go.

import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { dueNow } from './wardclock';

const PASSWORD = 'DemoCare2026!';

type Portrait = {
  name: string;
  answers: Record<string, string>;
  /** Очікуваний розмір основного тексту: 16px × масштаб. */
  scale: number;
  palette: 'standard' | 'color-safe';
  largeControls: boolean;
  dyslexicFont: boolean;
  dementiaMode: boolean;
  speech: boolean;
};

const portraits: Portrait[] = [
  {
    name: 'Марія, 78 — слабкий зір',
    answers: { visionText: 'yes' },
    scale: 200,
    palette: 'standard',
    largeControls: false,
    dyslexicFont: false,
    dementiaMode: false,
    speech: false,
  },
  {
    name: 'Іван, 82 — помірна деменція',
    answers: { visionText: 'partly', memory: 'yes' },
    scale: 150,
    palette: 'standard',
    largeControls: false,
    dyslexicFont: false,
    dementiaMode: true,
    speech: false,
  },
  {
    name: 'Ганна, 71 — тремор рук',
    answers: { motor: 'yes' },
    scale: 100,
    palette: 'standard',
    largeControls: true,
    dyslexicFont: false,
    dementiaMode: false,
    speech: false,
  },
  {
    name: 'Петро, 85 — дальтонізм',
    answers: { visionColors: 'yes', hearing: 'yes' },
    scale: 100,
    palette: 'color-safe',
    largeControls: false,
    dyslexicFont: false,
    dementiaMode: false,
    speech: false,
  },
  {
    name: 'Олена, 69 — дислексія',
    answers: { dyslexia: 'yes', prefersAudio: 'yes' },
    scale: 100,
    palette: 'standard',
    largeControls: false,
    dyslexicFont: true,
    dementiaMode: false,
    speech: true,
  },
  {
    name: 'Степан, 90 — поєднання обмежень',
    answers: {
      visionText: 'yes',
      visionColors: 'yes',
      hearing: 'yes',
      motor: 'yes',
      dyslexia: 'yes',
      memory: 'yes',
      prefersAudio: 'yes',
    },
    scale: 200,
    palette: 'color-safe',
    largeControls: true,
    dyslexicFont: true,
    dementiaMode: true,
    speech: true,
  },
  {
    name: 'Ніна, 75 — анкету пропущено',
    answers: {},
    scale: 100,
    palette: 'standard',
    largeControls: false,
    dyslexicFont: false,
    dementiaMode: false,
    speech: false,
  },
];

/** Готує підопічного через API та повертає токен його пристрою. */
async function prepareWard(api: APIRequestContext, person: Portrait): Promise<string> {
  const email = `portrait-${Date.now()}-${Math.floor(Math.random() * 10000)}@doglyad.local`;
  const registered = await api.post('/api/auth/register', {
    data: { name: 'Олена', email, password: PASSWORD, timezone: 'Europe/Kyiv' },
  });
  expect(registered.ok(), 'реєстрація помічника').toBeTruthy();

  const me = await (await api.get('/api/me')).json();
  const headers = { 'X-CSRF-Token': me.csrfToken };

  const ward = await api.post('/api/wards', {
    headers,
    data: { addressName: person.name, timezone: 'Europe/Kyiv', emergencyPhone: '103' },
  });
  expect(ward.ok(), 'створення профілю').toBeTruthy();
  const wardID = (await ward.json()).id;

  const needs = await api.put(`/api/wards/${wardID}/needs`, { headers, data: person.answers });
  expect(needs.ok(), 'анкета потреб').toBeTruthy();

  // Час завдання має вже настати, інакше відмітити його не можна.
  const due = dueNow();
  const task = await api.post(`/api/wards/${wardID}/tasks`, {
    headers,
    data: {
      title: 'Ранкові ліки',
      category: 'medicine',
      description: 'Одна таблетка зі склянкою води.',
      critical: true,
      localStartDate: due.date,
      localTime: due.time,
      recurrenceKind: 'daily',
      completionWindowMinutes: 720,
      escalationWindowMinutes: 600,
    },
  });
  expect(task.ok(), 'створення завдання').toBeTruthy();

  const pairing = await api.post(`/api/wards/${wardID}/pairing`, { headers, data: {} });
  expect(pairing.ok(), 'код підключення').toBeTruthy();
  const code = (await pairing.json()).code;

  const paired = await api.post('/api/pairing', { data: { code, label: 'Планшет' } });
  expect(paired.ok(), 'підключення пристрою').toBeTruthy();
  return (await paired.json()).deviceToken;
}

/** Відкриває екран підопічного на сторінці з підставленим токеном пристрою. */
async function openWardScreen(page: Page, deviceToken: string): Promise<void> {
  await page.addInitScript((token) => {
    window.localStorage.setItem('doglyad_device_token', token as string);
  }, deviceToken);
  await page.goto('/');
  await expect(page.locator('.ward-card')).toBeVisible();
}

/** Рахує контраст між двома кольорами за формулою WCAG. */
function contrastRatio(first: string, second: string): number {
  const luminance = (color: string): number => {
    const parts = color.match(/\d+(\.\d+)?/g);
    if (!parts || parts.length < 3) return 0;
    const channels = parts.slice(0, 3).map((value) => {
      const normalized = Number(value) / 255;
      return normalized <= 0.03928 ? normalized / 12.92 : ((normalized + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
  };
  const lighter = Math.max(luminance(first), luminance(second));
  const darker = Math.min(luminance(first), luminance(second));
  return (lighter + 0.05) / (darker + 0.05);
}

/**
 * findClippedText шукає текст, що не вміщається у свою ширину.
 * Саме так губиться назва справи на великому масштабі й вузькому екрані.
 */
async function findClippedText(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const selectors = [
      '.ward-name',
      '.ward-card h1',
      '.ward-time',
      '.ward-description',
      '.ward-main',
      '.ward-secondary',
      '.ward-help',
      '.ward-steps li',
      '.ward-message',
      '.ward-answer',
    ];
    const clipped: string[] = [];
    for (const selector of selectors) {
      document.querySelectorAll(selector).forEach((node) => {
        if (node.scrollWidth > node.clientWidth + 1) {
          clipped.push(`${selector}: «${node.textContent?.trim().slice(0, 30)}» ${node.scrollWidth}>${node.clientWidth}`);
        }
      });
    }
    return clipped;
  });
}

/** Повертає найближче непрозоре тло елемента. */
async function effectiveBackground(page: Page, selector: string): Promise<string> {
  return page.evaluate((target) => {
    let node: Element | null = document.querySelector(target);
    while (node) {
      const background = getComputedStyle(node).backgroundColor;
      if (background && !background.includes('rgba(0, 0, 0, 0)') && background !== 'transparent') {
        return background;
      }
      node = node.parentElement;
    }
    return 'rgb(255, 255, 255)';
  }, selector);
}

for (const person of portraits) {
  test(`екран підопічного: ${person.name}`, async ({ browser }) => {
    const context = await browser.newContext({
      locale: 'uk-UA',
      // Невеликий телефон — найсуворіша перевірка для великого тексту.
      viewport: { width: 360, height: 740 },
    });
    const api = await context.request;
    const deviceToken = await prepareWard(api, person);
    const page = await context.newPage();
    await openWardScreen(page, deviceToken);

    const main = page.locator('main.ward');

    // --- Масштаб тексту ---
    const fontSize = await main.evaluate((node) => parseFloat(getComputedStyle(node).fontSize));
    expect(fontSize, `масштаб ${person.scale}%`).toBeCloseTo((16 * person.scale) / 100, 1);

    // --- Палітра ---
    await expect(main).toHaveClass(new RegExp(`palette-${person.palette}`));

    // --- Шрифт для дислексії ---
    const fontFamily = await main.evaluate((node) => getComputedStyle(node).fontFamily);
    if (person.dyslexicFont) {
      expect(fontFamily.toLowerCase(), 'шрифт для дислексії').toContain('hyperlegible');
    } else {
      expect(fontFamily.toLowerCase(), 'звичайний шрифт').not.toContain('hyperlegible');
    }

    // --- Режим підказок: дата й кнопка «Я не пам'ятаю» ---
    await expect(page.locator('.ward-today')).toHaveCount(person.dementiaMode ? 1 : 0);
    await expect(page.getByRole('button', { name: /не пам.ятаю/ })).toHaveCount(person.dementiaMode ? 1 : 0);

    // --- Озвучення ---
    await expect(page.getByRole('button', { name: 'Прочитати вголос' })).toHaveCount(person.speech ? 1 : 0);

    // --- Режим помічника не має бути видимим нікому з підопічних ---
    await expect(page.getByRole('button', { name: 'Режим помічника' })).toHaveCount(0);

    // --- Розмір головної кнопки ---
    const mainButton = page.locator('.ward-main');
    const box = await mainButton.boundingBox();
    expect(box, 'головна кнопка має бути на екрані').not.toBeNull();
    const minimumHeight = person.largeControls ? 120 : 88;
    expect(box!.height, 'висота головної кнопки').toBeGreaterThanOrEqual(minimumHeight);

    // --- Усі кнопки доступні для натискання: не менше 44 точок ---
    const smallTargets = await page.evaluate(() => {
      const bad: string[] = [];
      document.querySelectorAll('main.ward button, main.ward a').forEach((node) => {
        const rect = node.getBoundingClientRect();
        if (rect.height > 0 && rect.height < 44) {
          bad.push(`${node.textContent?.trim().slice(0, 40)} — ${Math.round(rect.height)}px`);
        }
      });
      return bad;
    });
    expect(smallTargets, 'замалі цілі для натискання').toEqual([]);

    // --- Немає горизонтального прокручування ---
    const overflow = await page.evaluate(() => ({
      scroll: document.documentElement.scrollWidth,
      view: document.documentElement.clientWidth,
    }));
    expect(overflow.scroll, 'горизонтальне прокручування').toBeLessThanOrEqual(overflow.view + 1);

    // --- Контраст головної кнопки та тексту справи ---
    const buttonColors = await mainButton.evaluate((node) => {
      const style = getComputedStyle(node);
      return { text: style.color, background: style.backgroundColor };
    });
    expect(
      contrastRatio(buttonColors.text, buttonColors.background),
      'контраст тексту головної кнопки',
    ).toBeGreaterThanOrEqual(4.5);

    const titleColor = await page.locator('.ward-card h1').evaluate((node) => getComputedStyle(node).color);
    const cardBackground = await effectiveBackground(page, '.ward-card');
    expect(contrastRatio(titleColor, cardBackground), 'контраст назви справи').toBeGreaterThanOrEqual(7);

    // --- Жоден текст не обрізаний по ширині ---
    const clipped = await findClippedText(page);
    expect(clipped, 'текст не вміщається у відведену ширину').toEqual([]);

    // --- Порядок величин збережено: назва помітніша за час, час — за пояснення ---
    const sizes = await page.evaluate(() => {
      const sizeOf = (selector: string): number => {
        const node = document.querySelector(selector);
        return node ? parseFloat(getComputedStyle(node).fontSize) : 0;
      };
      return {
        title: sizeOf('.ward-card h1'),
        time: sizeOf('.ward-time'),
        description: sizeOf('.ward-description'),
        action: sizeOf('.ward-main'),
      };
    });
    expect(sizes.title, 'назва справи має бути найбільшою').toBeGreaterThan(sizes.time);
    expect(sizes.time, 'час має бути помітнішим за пояснення').toBeGreaterThanOrEqual(sizes.description);
    expect(sizes.action, 'напис на головній кнопці не має бути дрібнішим за пояснення').toBeGreaterThanOrEqual(
      sizes.description,
    );

    await page.screenshot({
      path: `test-results/portrait-${person.name.split(',')[0]}.png`,
      fullPage: true,
    });

    await context.close();
  });
}

test('підопічний із деменцією отримує відповідь застосунку без помічника', async ({ browser }) => {
  const person = portraits[1];
  const context = await browser.newContext({ locale: 'uk-UA', viewport: { width: 768, height: 1024 } });
  const deviceToken = await prepareWard(context.request, person);
  const page = await context.newPage();
  await openWardScreen(page, deviceToken);

  // Поки ліки не прийнято, питання йде помічникові.
  await page.getByRole('button', { name: /не пам.ятаю/ }).click();
  await expect(page.locator('.ward-message')).toContainText('запитали помічника');

  // Після відмітки застосунок відповідає сам.
  await page.locator('.ward-main').click();
  await expect(page.locator('.ward-message')).toContainText('Готово');
  await page.reload();
  await expect(page.locator('.ward-card')).toBeVisible();

  await context.close();
});


test('найважчий портрет на найвужчому екрані', async ({ browser }) => {
  const person = portraits[5];
  // 320 точок — найвужчий поширений екран телефона.
  const context = await browser.newContext({ locale: 'uk-UA', viewport: { width: 320, height: 568 } });
  const deviceToken = await prepareWard(context.request, person);
  const page = await context.newPage();
  await openWardScreen(page, deviceToken);

  const clipped = await findClippedText(page);
  expect(clipped, 'текст не вміщається на вузькому екрані').toEqual([]);

  const overflow = await page.evaluate(() => ({
    scroll: document.documentElement.scrollWidth,
    view: document.documentElement.clientWidth,
  }));
  expect(overflow.scroll, 'горизонтальне прокручування').toBeLessThanOrEqual(overflow.view + 1);

  // Навіть тут головна кнопка лишається великою ціллю.
  const box = await page.locator('.ward-main').boundingBox();
  expect(box!.height, 'висота головної кнопки').toBeGreaterThanOrEqual(120);

  await page.screenshot({ path: 'test-results/portrait-вузький-екран.png', fullPage: true });
  await context.close();
});


test('стан «сигнал надіслано» лишається читабельним', async ({ browser }) => {
  const person = portraits[0];
  const context = await browser.newContext({ locale: 'uk-UA', viewport: { width: 420, height: 900 } });
  const deviceToken = await prepareWard(context.request, person);
  const page = await context.newPage();
  await openWardScreen(page, deviceToken);

  await page.getByRole('button', { name: 'Потрібна допомога' }).click();
  const sent = page.getByRole('button', { name: /уже повідомлено/ });
  await expect(sent).toBeVisible();

  // Недоступна кнопка не має втрачати контраст: нею користується та сама людина.
  const colors = await sent.evaluate((node) => {
    const style = getComputedStyle(node);
    return { text: style.color, background: style.backgroundColor, opacity: Number(style.opacity) };
  });
  expect(colors.opacity, 'прозорість недоступної кнопки').toBe(1);
  expect(
    contrastRatio(colors.text, colors.background),
    'контраст кнопки після надсилання сигналу',
  ).toBeGreaterThanOrEqual(4.5);

  await context.close();
});
