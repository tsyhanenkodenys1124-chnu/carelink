// Сценарна перевірка через інтерфейс зі знімками «до» і «після».
//
// Кожну дію виконує людина у вікні браузера — жодних обхідних викликів API.
// У кожному сценарії знімки з обох екранів робляться одночасно: ліворуч те,
// що бачить помічник, праворуч — що бачить підопічний. Це дає пари «до/після»
// для пояснювальної записки і показує, що екрани справді пов'язані.
//
// Результат: docs/screens/<номер-сценарій>/{do,pislya}-{pomichnyk,pidopichnyi}.png

import { expect, test, type Page } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { dueNow } from './wardclock';

const PASSWORD = 'DemoCare2026!';
const SHOTS = path.resolve('../docs/screens');

/** Опис одного сценарію для підпису в галереї. */
type Scenario = { folder: string; title: string; note: string };

const captured: Scenario[] = [];

test.describe.configure({ mode: 'serial' });

test('історія користування: знімки до і після з обох екранів', async ({ browser }) => {
  // Помічник працює за комп'ютером, підопічний — за планшетом.
  const helperContext = await browser.newContext({ locale: 'uk-UA', viewport: { width: 1180, height: 900 } });
  const wardContext = await browser.newContext({ locale: 'uk-UA', viewport: { width: 420, height: 900 } });
  const helper = await helperContext.newPage();
  const ward = await wardContext.newPage();

  /** Знімає обидва екрани одночасно в одну папку сценарію. */
  const capture = async (scenario: Scenario, phase: 'do' | 'pislya') => {
    const folder = path.join(SHOTS, scenario.folder);
    await mkdir(folder, { recursive: true });
    await Promise.all([
      helper.screenshot({ path: path.join(folder, `${phase}-pomichnyk.png`), fullPage: true }),
      ward.screenshot({ path: path.join(folder, `${phase}-pidopichnyi.png`), fullPage: true }),
    ]);
    if (phase === 'do' && !captured.some((item) => item.folder === scenario.folder)) {
      captured.push(scenario);
    }
  };

  // --- Початковий стан: помічник реєструється, пристрій ще не підключено ---
  await ward.goto('/');
  await expect(ward.locator('.auth-card')).toBeVisible();

  await helper.goto('/');
  await helper.getByRole('button', { name: 'Створити обліковий запис' }).click();
  await helper.fill('input[name="name"]', 'Олена Коваль');
  await helper.fill('input[name="email"]', `demo-${Date.now()}@doglyad.local`);
  await helper.fill('input[name="password"]', PASSWORD);
  await helper.getByRole('button', { name: 'Створити обліковий запис' }).click();
  await expect(helper.locator('.shell')).toBeVisible();

  // === 1. Створення профілю підопічного ===
  const scenarioWard: Scenario = {
    folder: '01-stvorennya-profilyu',
    title: 'Створення профілю підопічного',
    note: 'Помічник заводить профіль. Пристрій підопічного ще не підключено і просить код.',
  };
  await capture(scenarioWard, 'do');

  await helper.getByRole('button', { name: 'Створити профіль' }).click();
  await helper.fill('input[name="addressName"]', 'Маріє Іванівно');
  await helper.fill('input[name="emergencyPhone"]', '+380 44 123 45 67');
  await helper.getByRole('button', { name: 'Зберегти профіль' }).click();
  await expect(helper.locator('.shell-title h1')).toHaveText('Маріє Іванівно');
  await capture(scenarioWard, 'pislya');

  // === 2. Додавання щоденної справи ===
  const scenarioTask: Scenario = {
    folder: '02-dodavannya-spravy',
    title: 'Додавання щоденної справи',
    note: 'Помічник вносить ранкові ліки. На екрані підопічного справи ще немає.',
  };
  await helper.getByRole('button', { name: 'Розклад' }).click();
  await expect(helper.locator('.panel-head h2')).toHaveText('Розклад');
  await capture(scenarioTask, 'do');

  await helper.getByRole('button', { name: 'Додати завдання' }).click();
  await helper.getByLabel('Коротка назва').fill('Ранкові ліки');
  await helper.getByLabel('Категорія').selectOption('medicine');
  await helper.getByLabel('Пояснення для підопічного').fill('Одна таблетка зі склянкою води.');
  // Час завдання має вже настати: далі сценарій показує саме відмітку виконання.
  const due = dueNow();
  await helper.getByLabel('Дата початку').fill(due.date);
  await helper.getByLabel('Час', { exact: true }).fill(due.time);
  await helper.getByLabel('Повторення').selectOption('daily');
  await helper.getByLabel(/Критична справа/).check();
  await helper.getByRole('button', { name: 'Створити завдання' }).click();
  await expect(helper.locator('.backdrop')).toHaveCount(0);
  await expect(helper.locator('.row-list > li').first()).toBeVisible();
  await capture(scenarioTask, 'pislya');

  // === 3. Підключення пристрою підопічного ===
  const scenarioPairing: Scenario = {
    folder: '03-pidkluchennya-prystroyu',
    title: 'Підключення пристрою підопічного',
    note: 'Помічник створює код і QR. Підопічний вводить шість цифр — і більше ніколи не входить у застосунок.',
  };
  await helper.getByRole('button', { name: 'Пристрої' }).click();
  await helper.getByRole('button', { name: 'Створити код' }).click();
  await expect(helper.locator('.pairing-code')).toBeVisible();
  const code = (await helper.locator('.pairing-code').innerText()).trim();

  // Картка PIN колись стискалася до ширини однієї колонки сітки,
  // і текст у ній переносився по одному слову.
  const pinCard = await helper.locator('.card', { hasText: 'PIN режиму помічника' }).boundingBox();
  expect(pinCard!.width, 'ширина картки PIN').toBeGreaterThan(300);

  await ward.getByRole('button', { name: 'Підключити пристрій підопічного' }).click();
  await expect(ward.locator('input[name="code"]')).toBeVisible();
  await capture(scenarioPairing, 'do');

  await ward.fill('input[name="code"]', code);
  await ward.getByRole('button', { name: 'Підключити пристрій' }).click();
  await expect(ward.locator('.ward-card')).toBeVisible();
  await helper.reload();
  await helper.getByRole('button', { name: 'Пристрої' }).click();
  await expect(helper.locator('.row-list > li').first()).toBeVisible();
  await capture(scenarioPairing, 'pislya');

  // === 4. Адаптація екрана за анкетою потреб ===
  const scenarioNeeds: Scenario = {
    folder: '04-anketa-potreb',
    title: 'Адаптація екрана за анкетою потреб',
    note: 'Ключовий сценарій роботи: помічник відповідає на побутові питання, а екран підопічного змінюється сам.',
  };
  await helper.getByRole('button', { name: 'Потреби' }).click();
  await expect(helper.locator('.needs-list')).toBeVisible();
  await capture(scenarioNeeds, 'do');

  for (const [question, answer] of [
    ['Важко читати звичайний текст', 'Так'],
    ['Важко влучати в невеликі кнопки', 'Так'],
    ['Забуває зроблене або поточний день', 'Так'],
  ] as const) {
    await helper
      .locator('.needs-list li', { hasText: question })
      .getByRole('button', { name: answer, exact: true })
      .click();
  }
  await helper.getByRole('button', { name: 'Зберегти та застосувати' }).click();
  await expect(helper.locator('.changes-card')).toBeVisible();
  // Пристрій підопічного оновлює екран сам; у знімку не чекаємо півхвилини.
  await ward.reload();
  await expect(ward.locator('.ward-card')).toBeVisible();
  await capture(scenarioNeeds, 'pislya');

  // === 5. Підопічний відмічає виконання ===
  const scenarioMark: Scenario = {
    folder: '05-vidmitka-vykonannya',
    title: 'Підопічний відмічає виконання',
    note: 'Одне натискання великої кнопки. У помічника статус змінюється без дзвінків і запитань.',
  };
  await helper.getByRole('button', { name: 'Розклад' }).click();
  await expect(helper.locator('.row-list > li').first()).toBeVisible();
  await capture(scenarioMark, 'do');

  await ward.locator('.ward-main').click();
  await expect(ward.locator('.ward-message')).toContainText('Готово');
  await helper.getByRole('button', { name: 'Огляд' }).click();
  await helper.getByRole('button', { name: 'Розклад' }).click();
  await expect(helper.locator('.pill-done').first()).toBeVisible();
  await capture(scenarioMark, 'pislya');

  // === 6. Скасування помилкового натискання ===
  const scenarioUndo: Scenario = {
    folder: '06-skasuvannya-vidmitky',
    title: 'Скасування помилкового натискання',
    note: 'Поки діє вікно скасування, підопічний виправляє помилку сам, не турбуючи помічника.',
  };
  await capture(scenarioUndo, 'do');

  await ward.getByRole('button', { name: /помилково/ }).click();
  await expect(ward.locator('.ward-message')).toContainText('Скасовано');
  await helper.getByRole('button', { name: 'Огляд' }).click();
  await helper.getByRole('button', { name: 'Розклад' }).click();
  await expect(helper.locator('.pill-planned').first()).toBeVisible();
  await capture(scenarioUndo, 'pislya');

  // === 7. Сигнал «Потрібна допомога» ===
  const scenarioHelp: Scenario = {
    folder: '07-potribna-dopomoga',
    title: 'Сигнал «Потрібна допомога»',
    note: 'Підопічний натискає одну кнопку. Банер у помічника з\'являється сам, без перезавантаження сторінки.',
  };
  await helper.getByRole('button', { name: 'Огляд' }).click();
  await expect(helper.locator('.signal-banner')).toHaveCount(0);
  await capture(scenarioHelp, 'do');

  await ward.getByRole('button', { name: 'Потрібна допомога' }).click();
  await expect(ward.locator('.ward-message')).toContainText('повідомили помічників');
  await expect(helper.locator('.signal-banner')).toBeVisible({ timeout: 15000 });
  await capture(scenarioHelp, 'pislya');

  // === 8. Помічник закриває сигнал ===
  const scenarioClose: Scenario = {
    folder: '08-zakryttya-sygnalu',
    title: 'Помічник закриває сигнал',
    note: 'Після відповіді помічника кнопка допомоги на пристрої знову доступна.',
  };
  await helper.locator('.signal-banner').click();
  await expect(helper.locator('.signal-card')).toHaveCount(1);
  await capture(scenarioClose, 'do');

  await helper.getByRole('button', { name: 'Питання вирішено' }).click();
  await expect(helper.locator('.signal-card')).toHaveCount(0);
  await ward.reload();
  await expect(ward.locator('.ward-card')).toBeVisible();
  await capture(scenarioClose, 'pislya');

  // === 9. «Я не пам'ятаю»: питання і відповідь ===
  const scenarioUnsure: Scenario = {
    folder: '09-ya-ne-pamyatayu',
    title: '«Я не пам\'ятаю»: питання і відповідь',
    note: 'Кнопка з\'явилася через режим підказок. Ліворуч — питання чекає на помічника, праворуч — його відповідь великим текстом на екрані підопічного.',
  };

  // Знімок «до» робимо вже з відкритим питанням: інакше обидва кадри показували б
  // порожню панель і пара втратила б сенс.
  await ward.getByRole('button', { name: /не пам.ятаю/ }).click();
  await expect(ward.locator('.ward-message')).toContainText('запитали помічника');
  await helper.getByRole('button', { name: 'Сигнали' }).click();
  await expect(helper.locator('.signal-card')).toHaveCount(1);
  await capture(scenarioUnsure, 'do');

  await helper.getByRole('button', { name: 'Відповісти' }).click();
  await helper.getByRole('button', { name: 'Ще не приймали. Прийміть зараз, будь ласка.' }).click();
  await helper.locator('.checkbox input').uncheck();
  await helper.getByRole('button', { name: 'Надіслати відповідь' }).click();
  await expect(helper.locator('.signal-card')).toHaveCount(0);
  await ward.reload();
  await expect(ward.locator('.ward-answer')).toBeVisible();
  await capture(scenarioUnsure, 'pislya');

  // === 10. Звіт із показниками ===
  const scenarioReport: Scenario = {
    folder: '10-zvit-pokaznyky',
    title: 'Звіт із показниками роботи',
    note: 'Числа, якими перевіряється досягнення мети: час реакції, виконання з першого разу, заблоковані повторні приймання.',
  };
  await helper.getByRole('button', { name: 'Огляд' }).click();
  await capture(scenarioReport, 'do');

  await helper.getByRole('button', { name: 'Звіт' }).click();
  await expect(helper.locator('.metrics-list')).toBeVisible();
  await capture(scenarioReport, 'pislya');

  // === 11. Режим помічника на пристрої підопічного ===
  const scenarioPIN: Scenario = {
    folder: '11-rezhym-pomichnyka',
    title: 'Режим помічника на пристрої підопічного',
    note: 'Помічник, який стоїть поруч, відмічає справу просто на планшеті за PIN-кодом. Налаштувань доступу тут немає навмисно.',
  };
  // PIN задає головний помічник у розділі «Пристрої».
  await helper.getByRole('button', { name: 'Пристрої' }).click();
  await helper.getByLabel('Новий PIN').fill('4821');
  await helper.getByRole('button', { name: 'Зберегти PIN' }).click();
  await expect(helper.locator('.message-notice')).toContainText('PIN збережено');

  // Режим відкривається утриманням імені вгорі екрана: кнопки там немає навмисно.
  await ward.reload();
  await expect(ward.locator('.ward-card')).toBeVisible();
  const wardName = ward.locator('.ward-name');
  await wardName.hover();
  await ward.mouse.down();
  await ward.waitForTimeout(2300);
  await ward.mouse.up();
  await expect(ward.locator('.pin-dialog')).toBeVisible();
  await ward.fill('input[name="pin"]', '4821');
  await capture(scenarioPIN, 'do');

  await ward.getByRole('button', { name: 'Відкрити' }).click();
  await expect(ward.locator('.pin-ward')).toBeVisible();
  await capture(scenarioPIN, 'pislya');

  await ward.getByRole('button', { name: 'Вийти з режиму помічника' }).click();
  await expect(ward.locator('.pin-dialog')).toHaveCount(0);

  // Опис сценаріїв поруч зі знімками — щоб галерея будувалася без ручної праці.
  await mkdir(SHOTS, { recursive: true });
  await writeFile(path.join(SHOTS, 'scenarios.json'), JSON.stringify(captured, null, 2) + '\n', 'utf8');

  await helperContext.close();
  await wardContext.close();
});
