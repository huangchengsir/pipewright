const searchBox = document.querySelector('#search-box');
const search = document.querySelector('#search');
const results = document.querySelector('#results');
const matches = document.querySelector('#search-matches');
const sections = [...document.querySelectorAll('.doc-section, .doc-topic')];
const entries = sections.map(section => {
  const content = section.cloneNode(true);
  content.querySelectorAll('.doc-topic').forEach(topic => topic.remove());
  const title = content.querySelector('h2, h3').textContent;
  const summary = (content.querySelector('p')?.textContent || content.textContent.trim())
    .replace(/\s+/g, ' ').slice(0, 120);
  return {
    id: section.id,
    title,
    heading: title.toLowerCase(),
    summary,
    terms: section.dataset.keywords?.toLowerCase() || '',
    text: content.textContent.toLowerCase()
  };
});

function renderSearch() {
  const words = search.value.trim().toLowerCase().split(/\s+/).filter(Boolean);
  matches.replaceChildren();
  matches.hidden = true;
  if (!words.length) {
    results.textContent = '';
    return;
  }

  const hits = [];
  for (const entry of entries) {
    const scores = words.map(word => {
      if (entry.heading.includes(word)) return 3;
      if (entry.terms.includes(word)) return 2;
      return Number(entry.text.includes(word));
    });
    if (scores.every(Boolean)) {
      hits.push({ ...entry, score: scores.reduce((sum, score) => sum + score, 0) });
    }
  }
  hits.sort((a, b) => b.score - a.score);

  results.textContent = hits.length ? '找到 ' + hits.length + ' 项' : '没有匹配的结果，请试试其他词。';
  for (const entry of hits) {
    const item = document.createElement('li');
    const link = document.createElement('a');
    link.href = '#' + entry.id;
    link.textContent = entry.title;
    const summary = document.createElement('p');
    summary.textContent = entry.summary;
    item.append(link, summary);
    matches.append(item);
  }
  matches.hidden = !hits.length;
}

searchBox.hidden = false;
search.addEventListener('input', renderSearch);
const sectionLinks = [...document.querySelectorAll('.sidebar a[href^="#"]')];

function updateCurrentSection() {
  const id = window.location.hash.slice(1) || 'quickstart';
  const section = document.getElementById(id)?.closest('.doc-section');
  for (const link of sectionLinks) {
    if (link.hash === '#' + section?.id) link.setAttribute('aria-current', 'location');
    else link.removeAttribute('aria-current');
  }
}

updateCurrentSection();
for (const link of sectionLinks) {
  link.addEventListener('click', () => {
    if (!search.value) return;
    search.value = '';
    renderSearch();
  });
}
window.addEventListener('hashchange', updateCurrentSection);

if (navigator.clipboard && window.isSecureContext) {
  document.querySelectorAll('.copy').forEach(button => {
    button.hidden = false;
    button.addEventListener('click', async () => {
      try {
        await navigator.clipboard.writeText(button.parentElement.querySelector('code').textContent);
        button.textContent = '已复制';
      } catch {
        button.textContent = '请手动复制';
      }
      window.setTimeout(() => { button.textContent = '复制'; }, 2000);
    });
  });
}
