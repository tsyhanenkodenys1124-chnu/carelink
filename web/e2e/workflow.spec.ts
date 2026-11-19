// Наскрізна перевірка застосунку в браузері.
//
// Тест самодостатній: він сам створює помічника, підопічного, завдання та пристрій,
// тому його можна запускати проти будь-якого запущеного сервера, не чекаючи демо-даних.
//
// Запуск:  npm run e2e   (сервер має бути запущений, типово http://127.0.0.1:8080)

import { expect, test, type Page } from '@playwright/test';
import { dueNow, laterToday } from './wardclock';

const PASSWORD = 'DemoCare2026!';

/** Унікальна адреса, щоб повторні запуски не конфліктували між собою. */
function uniqueEmail(): string {
  return `e2e-${Date.now()}-${Math.floor(Math.random() * 1000)}@doglyad.local`;
}

/** Створює обліковий запис помічника та входить у нього. */
async function registerHelper(page: Page): Promise<void> {
  await page.goto('/');
  await page.getByRole('button', { name: 'Створити обліковий запис' }).click();
  await page.fill('input[name="name"]', 'Олена');
  await page.fill('input[name="email"]', uniqueEmail());
  await page.fill('input[name="password"]', PASSWORD);
  await page.getByRole('button', { name: 'Створити обліковий запис' }).click();
  await expect(page.locator('.shell')).toBeVisible();
}

/** Створює профіль підопічного. */
async function createWard(page: Page, name: string): Promise<void> {
  await page.getByRole('button', { name: 'Створити профіль' }).click();
  await page.fill('input[name="addressName"]', name);
  await page.getByRole('button', { name: 'Зберегти профіль' }).click();
  await expect(page.locator('.shell-title h1')).toHaveText(name);
}

/** Додає щоденне завдання на вказану дату й час. */
async function createTask(page: Page, title: string, when: { date: string; time: string }): Promise<void> {
  await page.getByRole('button', { name: 'Розклад' }).click();
  await page.getByRole('button', { name: 'Додати завдання' }).click();
  await page.getByLabel('Коротка назва').fill(title);
  await page.getByLabel('Категорія').selectOption('food');
  await page.getByLabel('Дата початку').fill(when.date);
  await page.getByLabel('Час', { exact: true }).fill(when.time);
  await page.getByLabel('Повторення').selectOption('daily');
  await page.getByRole('button', { name: 'Створити завдання' }).click();
  await expect(page.locator('.backdrop')).toHaveCount(0);
}

/** Створює код підключення та повертає його. */
async function createPairingCode(page: Page): Promise<string> {
  await page.getByRole('button', { name: 'Пристрої' }).click();
  await page.getByRole('button', { name: 'Створити код' }).click();
  await expect(page.locator('.pairing-code')).toBeVisible();
  await expect(page.locator('.pairing-qr')).toBeVisible();
  return (await page.locator('.pairing-code').innerText()).trim();
}

test('помічник веде розклад, підопічний відмічає справи, сигнал приходить наживо', async ({ browser }) => {
  const helperContext = await browser.newContext({ locale: 'uk-UA' });
  const wardContext = await browser.newContext({ locale: 'uk-UA', viewport: { width: 820, height: 1180 } });
  const helper = await helperContext.newPage();
  const ward = await wardContext.newPage();

  await registerHelper(helper);
  await createWard(helper, 'Маріє Іванівно');
  await createTask(helper, 'Обід', dueNow());
  const code = await createPairingCode(helper);
  expect(code).toMatch(/^\d{6}$/);

  // Пристрій підопічного підключається кодом і показує одну справу.
  await ward.goto('/');
  await ward.getByRole('button', { name: 'Підключити пристрій підопічного' }).click();
  await ward.fill('input[name="code"]', code);
  await ward.getByRole('button', { name: 'Підключити пристрій' }).click();
  await expect(ward.locator('.ward-card h1')).toHaveText('Обід');
  // На екрані підопічного не має бути входу в режим помічника.
  await expect(ward.getByRole('button', { name: 'Режим помічника' })).toHaveCount(0);

  // Повертаємо помічника на огляд: банер має з'явитися сам, без перезавантаження.
  await helper.getByRole('button', { name: 'Огляд' }).click();
  await ward.getByRole('button', { name: 'Потрібна допомога' }).click();
  await expect(ward.locator('.ward-message')).toContainText('повідомили помічників');
  await expect(helper.locator('.signal-banner')).toBeVisible({ timeout: 15000 });

  // Помічник відкриває сигнал і закриває його.
  await helper.locator('.signal-banner').click();
  await expect(helper.locator('.signal-card')).toHaveCount(1);
  await helper.getByRole('button', { name: 'Питання вирішено' }).click();
  await expect(helper.locator('.signal-card')).toHaveCount(0);

  // Підопічний відмічає справу, потім скасовує помилкове натискання.
  await ward.reload();
  await ward.locator('.ward-main').click();
  await expect(ward.locator('.ward-message')).toContainText('Готово');
  // Поки можна скасувати, картка лишається про щойно виконану справу: інакше
  // «Готово» і кнопка скасування стояли б під назвою вже наступного завдання.
  await expect(ward.locator('.ward-label')).toHaveText('Виконано');
  await expect(ward.locator('.ward-card h1')).toHaveText('Обід');
  await expect(ward.locator('.ward-main')).toHaveCount(0);
  const undo = ward.getByRole('button', { name: /помилково/ });
  await expect(undo).toBeVisible();
  await undo.click();
  await expect(ward.locator('.ward-message')).toContainText('Скасовано');
  // Після скасування справа знову активна й доступна до виконання.
  await expect(ward.locator('.ward-label')).toHaveText('Зараз');
  await expect(ward.locator('.ward-main')).toBeVisible();

  // Звіт має показати показники роботи.
  await helper.getByRole('button', { name: 'Звіт' }).click();
  await expect(helper.locator('.metrics-list')).toBeVisible();
  await expect(helper.locator('.metrics-list')).toContainText('Реакція на «Потрібна допомога»');

  await helperContext.close();
  await wardContext.close();
});

test('анкета потреб пояснює кожну зміну екрана', async ({ page }) => {
  await registerHelper(page);
  await createWard(page, 'Іване Петровичу');

  await page.getByRole('button', { name: 'Потреби' }).click();
  // «Важко читати звичайний текст» — так.
  await page
    .locator('.needs-list li', { hasText: 'Важко читати звичайний текст' })
    .getByRole('button', { name: 'Так', exact: true })
    .click();
  await page.getByRole('button', { name: 'Зберегти та застосувати' }).click();

  await expect(page.locator('.changes-card')).toBeVisible();
  await expect(page.locator('.changes-list')).toContainText('Масштаб тексту: 200%');
  await expect(page.locator('.changes-list')).toContainText('важко читати звичайний текст');

  // Зміна має дійти до розділу «Екран».
  await page.getByRole('button', { name: 'Екран' }).click();
  await expect(page.getByLabel('Масштаб тексту')).toHaveValue('200');
});

test('справу, до якої ще години, не названо «Зараз» і відмітити її не можна', async ({ browser }) => {
  const helperContext = await browser.newContext({ locale: 'uk-UA' });
  const wardContext = await browser.newContext({ locale: 'uk-UA', viewport: { width: 820, height: 1180 } });
  const helper = await helperContext.newPage();
  const ward = await wardContext.newPage();

  await registerHelper(helper);
  await createWard(helper, 'Маріє Іванівно');
  await createTask(helper, 'Вечірні ліки', laterToday());
  const code = await createPairingCode(helper);

  await ward.goto('/');
  await ward.getByRole('button', { name: 'Підключити пристрій підопічного' }).click();
  await ward.fill('input[name="code"]', code);
  await ward.getByRole('button', { name: 'Підключити пристрій' }).click();
  await expect(ward.locator('.ward-card h1')).toHaveText('Вечірні ліки');

  // Підпис чесний, а головної кнопки немає: інакше ліки прийняли б за п'ять годин
  // до часу, вчасно їх ніхто б не випив, а помічник бачив би «виконано».
  await expect(ward.locator('.ward-label')).toHaveText('Наступна справа');
  await expect(ward.locator('.ward-main')).toHaveCount(0);
  await expect(ward.locator('.ward-wait')).toContainText('Поки нічого робити не треба');

  // Кнопка допомоги лишається доступною: людині може знадобитися допомога будь-коли.
  await expect(ward.getByRole('button', { name: 'Потрібна допомога' })).toBeEnabled();

  await helperContext.close();
  await wardContext.close();
});

test('режим помічника за PIN працює на пристрої підопічного', async ({ browser }) => {
  const helperContext = await browser.newContext({ locale: 'uk-UA' });
  const wardContext = await browser.newContext({ locale: 'uk-UA', viewport: { width: 820, height: 1180 } });
  const helper = await helperContext.newPage();
  const ward = await wardContext.newPage();

  await registerHelper(helper);
  await createWard(helper, 'Маріє Іванівно');
  await createTask(helper, 'Обід', dueNow());

  // Головний помічник задає PIN і підключає пристрій.
  await helper.getByRole('button', { name: 'Пристрої' }).click();
  await helper.getByLabel('Новий PIN').fill('4821');
  await helper.getByRole('button', { name: 'Зберегти PIN' }).click();
  await expect(helper.locator('.message-notice')).toContainText('PIN збережено');
  const code = await createPairingCode(helper);

  await ward.goto('/');
  await ward.getByRole('button', { name: 'Підключити пристрій підопічного' }).click();
  await ward.fill('input[name="code"]', code);
  await ward.getByRole('button', { name: 'Підключити пристрій' }).click();
  await expect(ward.locator('.ward-card h1')).toHaveText('Обід');

  // Підопічний просить допомоги — сигнал має бути видно в режимі помічника.
  await ward.getByRole('button', { name: 'Потрібна допомога' }).click();
  await expect(ward.locator('.ward-message')).toContainText('повідомили помічників');

  // Режим відкривається утриманням імені: кнопки немає навмисно.
  const name = ward.locator('.ward-name');
  await name.hover();
  await ward.mouse.down();
  await ward.waitForTimeout(2300);
  await ward.mouse.up();
  await expect(ward.locator('.pin-dialog')).toBeVisible();

  await ward.fill('input[name="pin"]', '4821');
  await ward.getByRole('button', { name: 'Відкрити' }).click();
  await expect(ward.locator('.pin-ward')).toHaveText('Маріє Іванівно');

  // Сигнал видно й він закривається звідси.
  await expect(ward.locator('.pin-list').first()).toContainText('Потрібна допомога');
  await ward.getByRole('button', { name: 'Питання вирішено' }).click();
  await expect(ward.locator('.message-notice')).toContainText('Сигнал закрито');

  // Справу дня можна відмітити виконаною.
  await ward.getByRole('button', { name: 'Відмітити виконаним' }).click();
  await expect(ward.locator('.message-notice')).toContainText('Справу відмічено');
  await expect(ward.getByRole('button', { name: 'Скасувати відмітку' })).toBeVisible();

  // Налаштувань доступу тут немає й бути не повинно.
  await expect(ward.locator('.pin-dialog')).toContainText('з PIN-коду вони не відкриваються');
  await expect(ward.getByRole('button', { name: 'Створити код' })).toHaveCount(0);

  // Вихід закриває режим; екран підопічного повертається.
  await ward.getByRole('button', { name: 'Вийти з режиму помічника' }).click();
  await expect(ward.locator('.pin-dialog')).toHaveCount(0);
  await expect(ward.locator('.ward-card')).toBeVisible();

  // Помічник на своєму пристрої бачить результат: відкритих сигналів немає.
  await helper.getByRole('button', { name: 'Огляд' }).click();
  await expect(helper.locator('.card', { hasText: 'Сигнали від підопічного' })).toContainText(
    'Відкритих сигналів немає',
    { timeout: 15000 },
  );

  await helperContext.close();
  await wardContext.close();
});
