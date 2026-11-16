const searchBox = document.querySelector('#search-box');
const search = document.querySelector('#search');
const results = document.querySelector('#results');
const sections = [...document.querySelectorAll('.doc-section')];
const keywords = sections.map(section => section.textContent.toLowerCase());

function filterSections() {
  const words = search.value.trim().toLowerCase().split(/\s+/).filter(Boolean);
  let matches = 0;
  sections.forEach((section, index) => {
    const match = words.every(word => keywords[index].includes(word));
    section.hidden = !match;
    if (match) matches += 1;
  });
  results.textContent = words.length ? (matches ? '找到 ' + matches + ' 个章节' : '没有匹配的章节，请试试其他词。') : '';
}

searchBox.hidden = false;
search.addEventListener('input', filterSections);
document.querySelectorAll('.sidebar a').forEach(link => link.addEventListener('click', () => {
  if (!search.value) return;
  search.value = '';
  filterSections();
}));
window.addEventListener('hashchange', () => {
  if (!search.value) return;
  search.value = '';
  filterSections();
});

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
