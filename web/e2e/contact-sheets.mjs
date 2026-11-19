// Збирає галерею знімків і зведений лист «до / після» для кожного сценарію.
//
// Запуск після сценарної перевірки:
//   node e2e/contact-sheets.mjs
//
// На вхід бере docs/screens/<сценарій>/{do,pislya}-{pomichnyk,pidopichnyi}.png,
// на вихід кладе docs/screens/index.html та <сценарій>/zvedennya.png —
// один знімок, який зручно вставити в пояснювальну записку.

import { chromium } from 'playwright';
import { readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const SHOTS = path.resolve('../docs/screens');
const executablePath = process.env.CHROMIUM_PATH;

const scenarios = JSON.parse(await readFile(path.join(SHOTS, 'scenarios.json'), 'utf8'));

/**
 * Розмітка зведеного листа: два стовпці «до» і «після», у кожному згори екран
 * помічника, знизу — екран підопічного. Так знімок помічника лишається
 * читабельним, а зв'язок між двома екранами видно в межах одного стовпця.
 */
function sheetHTML(scenario) {
  return `<!doctype html>
<html lang="uk">
<head>
<meta charset="utf-8">
<style>
  * { box-sizing: border-box; }
  body {
    margin: 0;
    padding: 30px;
    width: 2000px;
    background: #f6f9f8;
    color: #17373a;
    font-family: Inter, ui-sans-serif, system-ui, 'Segoe UI', sans-serif;
  }
  h1 { font-size: 30px; margin: 0 0 6px; }
  .note { color: #5b6f71; margin: 0 0 22px; font-size: 17px; max-width: 130ch; }
  .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 22px; align-items: start; }
  .cell { background: #fff; border: 1px solid #d9e6e5; border-radius: 16px; padding: 18px; }
  .cell > h2 {
    font-size: 16px; margin: 0 0 14px; text-transform: uppercase;
    letter-spacing: .1em; color: #5b6f71;
  }
  .cell.after > h2 { color: #1f6b53; }
  figure { margin: 0 0 16px; }
  figure:last-child { margin-bottom: 0; }
  figcaption { font-size: 14px; font-weight: 650; color: #5b6f71; margin-bottom: 8px; }
  img { border: 1px solid #e6eeed; border-radius: 10px; display: block; }
  .helper img { width: 100%; }
  .ward img { width: 420px; margin: 0 auto; }
</style>
</head>
<body>
  <h1>${scenario.title}</h1>
  <p class="note">${scenario.note}</p>
  <div class="grid">
    <section class="cell">
      <h2>До</h2>
      <figure class="helper"><figcaption>Помічник</figcaption><img src="do-pomichnyk.png" alt=""></figure>
      <figure class="ward"><figcaption>Підопічний</figcaption><img src="do-pidopichnyi.png" alt=""></figure>
    </section>
    <section class="cell after">
      <h2>Після</h2>
      <figure class="helper"><figcaption>Помічник</figcaption><img src="pislya-pomichnyk.png" alt=""></figure>
      <figure class="ward"><figcaption>Підопічний</figcaption><img src="pislya-pidopichnyi.png" alt=""></figure>
    </section>
  </div>
</body>
</html>`;
}

/** Галерея з посиланнями на всі сценарії. */
function galleryHTML() {
  const cards = scenarios
    .map(
      (scenario, index) => `  <article>
    <h2>${index + 1}. ${scenario.title}</h2>
    <p>${scenario.note}</p>
    <a href="${scenario.folder}/zvedennya.png"><img src="${scenario.folder}/zvedennya.png" alt="${scenario.title}"></a>
    <p class="files">Окремі знімки: <a href="${scenario.folder}/do-pomichnyk.png">до · помічник</a> ·
      <a href="${scenario.folder}/do-pidopichnyi.png">до · підопічний</a> ·
      <a href="${scenario.folder}/pislya-pomichnyk.png">після · помічник</a> ·
      <a href="${scenario.folder}/pislya-pidopichnyi.png">після · підопічний</a></p>
  </article>`,
    )
    .join('\n');

  return `<!doctype html>
<html lang="uk">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Догляд — знімки сценаріїв</title>
<style>
  body { margin: 0; padding: 32px; background: #f6f9f8; color: #17373a;
         font-family: Inter, ui-sans-serif, system-ui, 'Segoe UI', sans-serif; line-height: 1.5; }
  h1 { font-size: 30px; margin: 0 0 6px; }
  .lead { color: #5b6f71; max-width: 90ch; margin: 0 0 28px; }
  article { background: #fff; border: 1px solid #d9e6e5; border-radius: 14px; padding: 20px; margin-bottom: 22px; }
  article h2 { font-size: 20px; margin: 0 0 4px; }
  article p { color: #5b6f71; margin: 0 0 12px; }
  article img { width: 100%; border: 1px solid #e6eeed; border-radius: 10px; display: block; }
  .files { font-size: 14px; margin-top: 10px; }
  a { color: #125c63; }
</style>
</head>
<body>
  <h1>Знімки сценаріїв застосунку «Догляд»</h1>
  <p class="lead">Кожен сценарій знято через інтерфейс: ліворуч стан до дії, праворуч — після.
    У межах пари обидва екрани зняті одночасно, тож видно, як дія на одному боці відображається на іншому.</p>
${cards}
</body>
</html>`;
}

const browser = await chromium.launch(executablePath ? { executablePath } : {});
const page = await browser.newPage({ viewport: { width: 2000, height: 1000 } });

for (const scenario of scenarios) {
  const folder = path.join(SHOTS, scenario.folder);
  const file = path.join(folder, 'zvedennya.html');
  await writeFile(file, sheetHTML(scenario), 'utf8');
  await page.goto(pathToFileURL(file).href, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(folder, 'zvedennya.png'), fullPage: true });
  console.log(`зведення: ${scenario.folder}`);
}

await writeFile(path.join(SHOTS, 'index.html'), galleryHTML(), 'utf8');
console.log(`галерея: ${path.join(SHOTS, 'index.html')}`);

await browser.close();
