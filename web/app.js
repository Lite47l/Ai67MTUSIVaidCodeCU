'use strict';
const root=document.querySelector('#app');
const S = {
  who: null, schema: null, view: 'overview', month: 6, person: null, profile: null,
  overview: null, tab: 'history', search: '', metric: 'workload', integrationSummary: null,
  renderGeneration: 0,
};
const MONTHS = [1, 2, 3, 4, 5, 6];
const escapeHTML=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const E=escapeHTML;
const statusText = {positive: 'Норма', attention: 'Требует внимания', critical: 'Критичный сигнал', missing: 'Недостаточно данных'};
const roles = {hr: 'Кабинет HR', mentor: 'Кабинет наставника', intern: 'Кабинет стажёра'};
const srcName = {intern: 'Стажёр', mentor: 'Наставник'};
const statuses = ['critical', 'attention', 'positive', 'missing'];
const queuePriority = ['critical', 'attention', 'missing', 'positive'];
const paths = {
  overview: '<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/>',
  actions: '<rect x="4" y="4" width="16" height="17" rx="2"/><path d="M8 4V2m8 2V2m-8 11 2 2 5-5"/>',
  integrations: '<path d="M8 3v5m8-5v5M5 8h14m-2 0v3a5 5 0 0 1-10 0V8m5 8v5"/>',
  enroll: '<circle cx="9" cy="7" r="3"/><path d="M3 20v-2a5 5 0 0 1 5-5h2a5 5 0 0 1 3 1m5-5v8m-4-4h8"/>',
  survey: '<rect x="5" y="3" width="14" height="18" rx="2"/><path d="M8 8h8m-8 4h8m-8 4h5"/>',
  exit: '<path d="M10 17l5-5-5-5m5 5H3m9-8h6a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-6"/>',
  month: '<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M7 3v4m10-4v4M3 10h18"/>',
  refresh: '<path d="M20 10a8 8 0 1 0-1 7m1-11v5h-5m-11 3a8 8 0 0 0 2 4"/>',
  search: '<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4 4"/>',
  right: '<path d="m9 6 6 6-6 6"/>',
  down: '<path d="m6 9 6 6 6-6"/>',
  positive: '<circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/>',
  attention: '<path d="M10.4 4.9 3 18a1 1 0 0 0 .9 1.5h16.2A1 1 0 0 0 21 18L13.6 4.9a1.8 1.8 0 0 0-3.2 0ZM12 9v4m0 3h.01"/>',
  critical: '<path d="m9 3-6 6v6l6 6h6l6-6V9l-6-6H9Zm3 5v5m0 3h.01"/>',
  missing: '<circle cx="12" cy="12" r="9"/><path d="M8 12h8"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5m0-8h.01"/>',
};
const icon = name => `<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name]}</svg>`;
const date = value => new Date(value).toLocaleString('ru-RU', {dateStyle: 'short', timeStyle: 'short'});
const badge = (status, inline = false) => `<span class="badge ${E(status)}${inline ? ' badge--inline' : ''}">${icon(paths[status] ? status : 'info')}${E(statusText[status] || status)}</span>`;
const options = (values, selected) => values.map(([value, label]) => `<option value="${E(value)}"${String(value) === String(selected) ? ' selected' : ''}>${E(label)}</option>`).join('');
const monthSelect = () => `<label class="period-control"><span>Контрольный период</span><span class="select-control">${icon('month')}<select id="month" aria-label="Контрольный период">${options(MONTHS.map(month => [month, `Месяц ${month} из 6`]), S.month)}</select>${icon('down')}</span></label>`;
const refreshButton = () => `<button type="button" class="secondary refresh-button" data-act="refresh">${icon('refresh')}Обновить данные</button>`;
const score = value => value == null ? '<span title="Нет оценки"><span aria-hidden="true">—</span><span class="sr-only">Нет оценки</span></span>' : E(value);
function toast(msg){const el=document.querySelector('#toast');el.textContent=msg;el.classList.add('visible');clearTimeout(toast.timer);toast.timer=setTimeout(()=>el.classList.remove('visible'),4500)}
async function api(path, method = 'GET', body) {
  let response;
  try {
    response = await fetch('/api/v1' + path, {
      method, credentials: 'same-origin',
      headers: body ? {'Content-Type': 'application/json'} : {},
      body: body ? JSON.stringify(body) : undefined,
    });
  } catch {
    throw new Error('Не удалось связаться с сервером. Проверьте соединение и повторите.');
  }
  let data;
  try {
    data = await response.json();
  } catch {
    throw Object.assign(new Error('Сервис вернул некорректный ответ.'), {status: response.status});
  }
  if (!response.ok) {
    if (response.status === 401 && S.who) {
      S.who = null;
      loginPage(true);
    }
    const message = typeof data.error === 'string' ? data.error : data.error?.message;
    throw Object.assign(new Error(message || `Ошибка ${response.status}`), {status: response.status});
  }
  return data;
}
function safeLink(url,label){try{const u=new URL(url);if(!['http:','https:'].includes(u.protocol))return '';return `<a href="${E(u.href)}" target="_blank" rel="noopener noreferrer">${E(label)}</a>`}catch{return ''}}
function shell(content) {
  const role = S.who.role;
  const overviewLayout = role === 'hr' && S.view === 'overview';
  const topbarLayout = overviewLayout || role !== 'hr';
  const nav = role === 'hr'
    ? [['overview', 'Обзор'], ['actions', 'Действия HR'], ['integrations', 'Интеграции'], ['enroll', 'Новый участник']]
    : [['overview', role === 'mentor' ? 'Мои стажёры' : 'Моя стажировка'], ['survey', 'Ежемесячная анкета']];
  const activeView = S.view === 'profile' ? 'overview' : S.view;
  const brand = '<div class="brand"><span class="brandmark">МГТС</span><strong>Пульс стажировки</strong></div>';
  const summary = S.integrationSummary;
  const integrationStatus = `<div class="topbar-status">${summary ? `<span>${icon('integrations')}Tally ${summary.tallyConfigured ? 'настроен' : 'не настроен'}</span>` : ''}
    <span>AI-агент ${S.overview?.agentConnected ? 'подключён' : 'не подключён'}</span></div>`;

  root.innerHTML = `<a class="skip-link" href="#main">К основному содержанию</a>
    <div class="layout ${overviewLayout ? 'layout--overview' : topbarLayout ? 'layout--role' : 'layout--detail'}">
      ${topbarLayout ? `<header class="topbar">${brand}${overviewLayout ? integrationStatus : ''}</header>` : ''}
      <aside class="sidebar">
        ${topbarLayout ? '' : brand}
        <nav aria-label="Основная навигация">
          ${nav.map(([view, label]) => `<button type="button" data-nav="${view}"${activeView === view ? ' aria-current="page"' : ''}>${icon(view)}<span>${label}</span></button>`).join('')}
        </nav>
        <footer><span class="role-label">${roles[role]}</span><button type="button" class="icon-button" data-act="logout" aria-label="Выйти" title="Выйти">${icon('exit')}</button></footer>
      </aside>
      <main class="main" id="main" tabindex="-1">
        ${topbarLayout ? '' : '<p class="program-label">Молодёжные программы</p>'}
        ${content}
      </main>
    </div>`;
}
function loginPage(expired = false) {
  root.innerHTML = `<div class="login">
    <section class="login-story" aria-label="Программа стажировок МГТС">
      <span class="brandmark">МГТС</span>
      <div class="login-brand-copy"><strong>Пульс стажировки</strong><p>Программа стажировок МГТС</p></div>
      <small>Молодёжные программы</small>
    </section>
    <main class="login-form">
      <div class="loginbox">
        <h1>Войти в программу</h1>
        <p>Используйте учётную запись, выданную администратором.</p>
        <form id="login">
          <div id="login-error" role="alert">${expired ? '<p class="notice"><strong>Сессия закончилась</strong><br>Войдите снова, чтобы продолжить работу.</p>' : ''}</div>
          <label>Логин<input name="username" autocomplete="username" required maxlength="100" placeholder="Введите логин"></label>
          <label>Пароль<input name="password" type="password" autocomplete="current-password" required maxlength="256" placeholder="Введите пароль"></label>
          <div><button type="submit">Войти</button></div>
        </form>
      </div>
    </main>
  </div>`;
}
async function init() {
  try {
    S.who = await api('/me');
    await loadSession();
  } catch (error) {
    S.who = null;
    loginPage(error.status === 401 && sessionStorage.getItem('pulse:signed-in') === 'yes');
  }
}
async function loadSession() {
  S.schema = await api('/schema');
  S.questions = await (await fetch('/questions.json')).json();
  history.replaceState(null, '', '/' + S.who.role);
  sessionStorage.setItem('pulse:signed-in', 'yes');
  Object.assign(S, {
    person: S.schema.people[0]?.id, view: 'overview', tab: 'history',
    month: 6, overview: null, overviewError: '', integrationSummary: null,
    search: '', department: '', statusFilter: '', sort: 'priority',
    surveyResult: null,
  });
  await render();
}
async function render(refresh = false) {
  const generation = ++S.renderGeneration;
  const {who, view, month, person} = S;
  const isCurrent = () => generation === S.renderGeneration && S.who === who;
  try {
    if (view === 'integrations') {
      const data = await api('/hr/integrations');
      if (isCurrent()) {
        S.integrationSummary = data;
        integrations(data);
      }
    } else if (view === 'actions') {
      const list = await api('/hr/actions');
      if (isCurrent()) actions(list);
    } else if (view === 'enroll') {
      enroll();
    } else if (view === 'survey' || view === 'profile' || who.role === 'intern') {
      if (!person) throw new Error('К учётной записи не привязан стажёр');
      const data = await api(`/${who.role}/interns/${encodeURIComponent(person)}?month=${month}`);
      if (isCurrent()) {
        S.profile = data;
        if (view === 'survey') survey();
        else if (view === 'overview' && who.role === 'intern') internOverview();
        else profile();
      }
    } else if (who.role === 'hr') {
      const [data, details] = await Promise.all([
        api('/hr/overview?month=' + month),
        refresh || !S.integrationSummary ? api('/hr/integrations').catch(() => null) : S.integrationSummary,
      ]);
      if (isCurrent()) {
        S.overview = data;
        S.integrationSummary = details;
        S.overviewError = '';
        S.overviewTime = new Date().toLocaleTimeString('ru-RU', {hour: '2-digit', minute: '2-digit'});
        overview();
      }
    } else {
      mentorOverview();
    }
  } catch (err) {
    if (!isCurrent()) return;
    if (who.role === 'hr' && view === 'overview' && S.overview) {
      S.overviewError = err.message;
      overview();
    } else {
      shell(`<h1>${err.status === 404 && view === 'profile' ? 'Профиль недоступен' : 'Не удалось загрузить данные'}</h1><p class="error" role="alert">${E(err.message)}</p><button data-act="refresh">Повторить</button>`);
    }
  }
}
function head(title, text, controls = true) {
  return `<div class="pagehead"><div><h1>${E(title)}</h1>${text ? `<p>${E(text)}</p>` : ''}</div>
    ${controls ? `<div class="controls">${monthSelect()}${refreshButton()}</div>` : ''}</div>`;
}
function detailHead(title, action = '') {
  return `<header class="detail-head"><div><p class="eyebrow">Кабинет HR</p><h1>${title}</h1></div>${action}</header>`;
}
function rankSignals(signals) {
  return [...signals].sort((a, b) => statuses.indexOf(a.severity) - statuses.indexOf(b.severity));
}
function surveyStatus(submitted) {
  const status = submitted ? 'positive' : 'missing';
  return `<span class="survey-status ${status}">${icon(status)}${submitted ? 'Получена' : 'Нет ответа'}</span>`;
}
function overviewResults() {
  const term = S.search.trim().toLocaleLowerCase('ru-RU');
  const rows = S.overview.items
    .filter(row => (!S.department || row.person.department === S.department) && (!S.statusFilter || row.status === S.statusFilter))
    .filter(({person}) => (person.id + ' ' + person.department).toLocaleLowerCase('ru-RU').includes(term))
    .sort((a, b) => {
      const idOrder = a.person.id.localeCompare(b.person.id, 'ru');
      if (S.sort === 'id') return idOrder;
      if (S.sort === 'department') return a.person.department.localeCompare(b.person.department, 'ru') || idOrder;
      return queuePriority.indexOf(a.status) - queuePriority.indexOf(b.status) || idOrder;
    });
  if (!rows.length) {
    return `<p class="queue-empty">${term ? 'По этому запросу участники не найдены.' : S.department || S.statusFilter ? 'По этим фильтрам участники не найдены.' : 'В программе пока нет участников.'}</p>`;
  }
  const personButton = (row, className, label) => `<button type="button" class="${className}" data-person="${E(row.person.id)}" aria-label="Профиль стажёра ${E(row.person.id)}">${label}</button>`;
  const reason = (row, top) => top?.title || (row.status === 'missing' ? 'Нужны ответы или оценки' : '');
  const table = `<div class="tablewrap queue-table" role="region" aria-label="Таблица участников" tabindex="0">
    <table><thead><tr>${['ID стажёра', 'Подразделение', 'Статус', 'Причина сигнала', 'Анкета стажёра', 'Анкета наставника'].map(label => `<th scope="col">${label}</th>`).join('')}<th scope="col"><span class="sr-only">Профиль</span></th></tr></thead>
    <tbody>${rows.map(row => {
      const top = rankSignals(row.signals)[0];
      const source = /^low:(intern|mentor):/.exec(top?.id || '')?.[1];
      return `<tr class="row-${E(row.status)}"><th scope="row">${E(row.person.id)}</th><td>${E(row.person.department)}</td><td>${badge(row.status)}</td>
        <td>${E(reason(row, top) || '—')}${source ? `<span class="secondarytext">Анкета ${source === 'intern' ? 'стажёра' : 'наставника'}</span>` : ''}</td>
        <td>${surveyStatus(row.internSubmitted)}${ratingAverage(row.internAverage)}</td><td>${surveyStatus(row.mentorSubmitted)}${ratingAverage(row.mentorAverage)}</td><td>${personButton(row, 'link-button row-link', `Профиль ${icon('right')}`)}</td></tr>`;
    }).join('')}</tbody></table></div>`;
  const list = [['Требуют внимания', rows.filter(row => ['critical', 'attention'].includes(row.status))],
    ['Недостаточно данных', rows.filter(row => row.status === 'missing')], ['Без активных сигналов', rows.filter(row => row.status === 'positive')]]
    .filter(([, group]) => group.length).map(([label, group]) => `<section class="queue-group"><h3>${label} <small>${group.length}</small></h3><ul>
      ${group.map(row => `<li class="row-${E(row.status)}"><div>${personButton(row, 'link-button', E(row.person.id))}<small>${E(row.person.department)}</small></div>
        <div class="queue-reason">${E(reason(row, rankSignals(row.signals)[0]))}${row.status === 'positive' ? '' : badge(row.status, true)}</div>
        <div class="queue-forms"><small>${row.scoresComplete && row.internSubmitted && row.mentorSubmitted ? 'Обе анкеты получены' : `Стажёр: ${row.internSubmitted ? 'получена' : 'нет ответа'} · наставник: ${row.mentorSubmitted ? 'получена' : 'нет ответа'}${!row.scoresComplete ? ' · есть пропуски' : ''}`}</small></div></li>`).join('')}</ul></section>`).join('');
  return table + `<div class="queue-list">${list}</div>`;
}
function ratingAverage(value) {
  return value == null ? '' : `<small class="secondarytext">Средняя: ${Number(value).toLocaleString('ru-RU', {minimumFractionDigits: 1, maximumFractionDigits: 1})}</small>`;
}
function overview() {
  const data = S.overview;
  const total = data.items.length;
  const intern = data.items.filter(row => row.internSubmitted).length;
  const mentor = data.items.filter(row => row.mentorSubmitted).length;
  const attention = data.critical + data.attention;
  const departments = [...new Set(data.items.map(row => row.person.department))].sort((a, b) => a.localeCompare(b, 'ru'));
  const counts = {...data, missing: data.items.filter(row => row.status === 'missing').length};
  let segmentStart = 0;
  const segments = statuses.filter(status => counts[status] > 0).map(status => {
    const segment = `<rect class="${status}" x="${segmentStart}" width="${counts[status]}" height="1"/>`;
    segmentStart += counts[status];
    return segment;
  }).join('');
  const percent = value => total ? `${Math.round(100 * value / total)} % за месяц` : 'Нет участников';
  const kpis = [
    ['Участники', total, 'В программе'],
    ['Ответы стажёров', `${intern} из ${total}`, percent(intern)],
    ['Ответы наставников', `${mentor} из ${total}`, percent(mentor)],
    ['Требуют внимания', `${attention} из ${total}`, 'С активным сигналом'],
  ];
  shell(`<div class="overview">
    ${head('Обзор стажировок', 'Программа стажировок МГТС')}
    ${S.overviewError ? `<div class="notice warning" role="status"><strong>Не удалось обновить данные</strong><p>Показаны последние полученные данные за месяц ${data.month}. ${E(S.overviewError)}</p></div>` : ''}
    <dl class="stats">${kpis.map(([label, value, hint]) => `<div class="stat"><dt>${label}</dt><dd>${value}</dd><small>${hint}</small></div>`).join('')}</dl>
    <div class="overview-meta" aria-label="Сводные данные"><span>Анкеты за месяц <b>${data.submitted} из ${data.expected}</b></span><span>Неполные пары <b>${data.incomplete}</b></span><span>История <b>${data.totalResponses} ответов</b></span><span>Меры HR <b>${data.closedActions} из ${data.actions} закрыто</b></span></div>
    <section class="queue surface" aria-labelledby="queue-title">
      <div class="panelhead"><h2 id="queue-title">Очередь внимания</h2><small>${total} · месяц ${data.month}${S.overviewTime ? ' · обновлено в ' + S.overviewTime : ''}</small></div>
      <div class="overview-filters" aria-label="Фильтры и сортировка">
        <label class="search"><span class="sr-only">Поиск по ID или подразделению</span><span class="search-field">${icon('search')}<input type="search" id="search" placeholder="ID или подразделение" value="${E(S.search)}"></span></label>
        <label><span class="sr-only">Подразделение</span><select id="department-filter">${options([['', 'Все подразделения'], ...departments.map(department => [department, department])], S.department)}</select></label>
        <label><span class="sr-only">Статус</span><select id="status-filter">${options([['', 'Все статусы'], ...statuses.map(status => [status, statusText[status]])], S.statusFilter)}</select></label>
        <label class="sort-control"><span class="sr-only">Сортировка</span><select id="sort-filter">${options([['priority', 'По приоритету'], ['id', 'По ID стажёра'], ['department', 'По подразделению']], S.sort)}</select></label>
      </div>
      <div id="queue-results">${overviewResults()}</div>
    </section>
    <section class="distribution surface" aria-labelledby="status-title">
      <div class="panelhead"><h2 id="status-title">Распределение статусов</h2><small>Участников: ${total} · месяц ${data.month}</small></div>
      ${total ? `<svg class="distribution-bar" viewBox="0 0 ${total} 1" preserveAspectRatio="none" aria-hidden="true">${segments}</svg>` : ''}
      <ul class="legend">${statuses.map(status => `<li>${badge(status, true)} <b>${counts[status]}</b></li>`).join('')}</ul>
    </section>
    <p class="footnote">${icon('info')} Статусы рассчитаны по сигналам за выбранный месяц. Отсутствующие оценки сохраняются как пропуски.</p>
  </div>`);
}
function mentorOverview() {
  const people = S.schema.people;
  shell(`${head('Мои стажёры', 'Профили участников под вашим наставничеством', false)}
    <section class="panel role-panel"><h2>Назначенные стажёры</h2>
      ${people.length ? `<ul class="mentor-list">${people.map(person => `<li><div>
        <h3>${E(person.id)}</h3><p class="muted">${E(person.department)}</p>
        </div><button type="button" class="secondary" data-person="${E(person.id)}" aria-label="Открыть профиль ${E(person.id)}">Открыть профиль${icon('right')}</button></li>`).join('')}</ul>`
        : '<div class="empty"><h3>Нет назначенных стажёров</h3><p>Здесь появятся участники, закреплённые за вами в программе.</p></div>'}
    </section>
    ${people.length ? '<section class="panel role-panel"><h2>Ежемесячная анкета</h2><p class="muted">Откройте профиль нужного стажёра, чтобы заполнить его анкету за выбранный период.</p></section>' : ''}`);
}
function internOverview() {
  const data = S.profile;
  const {person, responses, feedback, month} = data;
  const latest = feedback[0];
  const surveyButton = '<button type="button" data-nav="survey">К анкете</button>';
  shell(`<div class="pagehead"><div><h1>Моя стажировка</h1><p>${E(person.id)} · ${E(person.department)}</p></div><div class="controls">${monthSelect()}${surveyButton}</div></div>
    <section class="panel role-panel"><h2>Контрольные точки</h2>
      <ul class="period-cards">${MONTHS.map(value => {
        const row = responseAt(responses, 'intern', value);
        return `<li><button type="button" class="subtle-button"${value === month ? ' aria-current="true"' : ''} data-period="${value}"><strong>Месяц ${value}</strong><small>${row ? 'Анкета получена' : 'Нет ответа'}</small></button></li>`;
      }).join('')}</ul>
    </section>
    <section class="panel role-panel"><div class="panelhead"><h2>Обратная связь наставника</h2>
      <button type="button" class="secondary" data-open-profile="feedback">Открыть сообщения</button></div>
      ${latest ? `<p>${E(latest.text)}</p>` : '<p class="muted">Опубликованных сообщений пока нет.</p>'}
    </section>
    <section class="panel role-panel"><div class="panelhead"><h2>История и динамика</h2><button type="button" class="secondary" data-open-profile="history">Открыть историю</button></div></section>`);
}
const comparisonMetrics = {
  clarity: {label: 'Понимание задач', fields: ['taskClarity', 'taskUnderstanding']},
  interaction: {label: 'Взаимодействие с командой', fields: ['teamwork', 'communication']},
};
const surveyTitles = {intern: 'Анкета стажёра', mentor: 'Анкета наставника'};
function responseAt(responses, source, month) {
  return responses.find(row => row.source === source && row.month === month);
}
function chart(responses) {
  const comparison = comparisonMetrics[S.metric];
  const fields = comparison?.fields || [S.metric];
  const series = fields.map(field => {
    const source = S.schema.mentorFields.includes(field) ? 'mentor' : 'intern';
    return {source, label: S.schema.labels[field], values: MONTHS.map(month => responseAt(responses, source, month)?.scores[field] ?? null)};
  });
  const x = index => 43 + index * 177;
  const y = value => 142 - (value - 1) * 27;
  const drawing = series.map(item => {
    const segments = [[]];
    const points = [];
    item.values.forEach((value, index) => {
      if (value == null) {
        if (segments.at(-1).length) segments.push([]);
        return;
      }
      const cx = x(index), cy = y(value);
      segments.at(-1).push(`${cx},${cy}`);
      const title = `<title>${srcName[item.source]} · месяц ${index + 1}: ${value} из 5</title>`;
      points.push(item.source === 'mentor'
        ? `<path class="chart-point" d="M${cx} ${cy - 6}l6 6-6 6-6-6Z">${title}</path>`
        : `<circle class="chart-point" cx="${cx}" cy="${cy}" r="4">${title}</circle>`);
    });
    return `<g class="chart-${item.source}">${segments.filter(segment => segment.length > 1).map(segment => `<polyline points="${segment.join(' ')}"/>`).join('')}${points.join('')}</g>`;
  }).join('');
  const metrics = [
    ['clarity', comparisonMetrics.clarity.label], ['interaction', comparisonMetrics.interaction.label],
    ...S.schema.internFields.map(field => [field, S.schema.labels[field]]),
    ...S.schema.mentorFields.map(field => [field, `Наставник: ${S.schema.labels[field]}`]),
  ];
  return `<section class="detail-section dynamics" aria-labelledby="chart-heading">
    <div class="panelhead">
      <h2 id="chart-heading">Динамика оценок</h2>
      <label class="select-control metric-control"><span class="sr-only">Показатель для графика</span><select id="metric">${options(metrics, S.metric)}</select>${icon('down')}</label>
    </div>
    ${comparison ? `<ul class="chart-legend">${series.map(item => `<li><i class="legend-${item.source}"></i>${srcName[item.source]} · ${E(item.label)}</li>`).join('')}</ul>`
      : `<p class="chart-description">${srcName[series[0].source]} · ${E(series[0].label)} · шкала от 1 до 5</p>`}
    <div class="chartbox" role="region" aria-label="График и таблица оценок" tabindex="0">
      <svg class="chart" viewBox="0 0 1010 173" role="img" aria-label="Динамика за шесть месяцев. Значения приведены в таблице ниже.">
        <rect class="chart-selection" x="${x(S.month - 1) - 20}" y="20" width="40" height="132" rx="4"/>
        ${[1, 2, 3, 4, 5].map(value => `<line class="chart-grid" x1="30" x2="953" y1="${y(value)}" y2="${y(value)}"/><text x="5" y="${y(value) + 4}">${value}</text>`).join('')}
        ${MONTHS.map((month, index) => `<text x="${x(index)}" y="164" text-anchor="middle"${month === S.month ? ' class="chart-active"' : ''}>Месяц ${month}</text>`).join('')}
        ${drawing}
      </svg>
      <table class="chart-values"><caption class="sr-only">Оценки по месяцам</caption>
        <thead><tr><th scope="col">Оценки по месяцам</th>${MONTHS.map(month => `<th scope="col"${month === S.month ? ' class="chart-active"' : ''}>${month}</th>`).join('')}</tr></thead>
        <tbody>${series.map(item => `<tr><th scope="row">${srcName[item.source]}</th>${item.values.map((value, index) => `<td${index + 1 === S.month ? ' class="chart-active"' : ''}>${score(value)}</td>`).join('')}</tr>`).join('')}</tbody>
      </table>
    </div>
  </section>`;
}
function signalHTML(signal, responses) {
  return `<article class="signal">${badge(signal.severity)}<h3>${E(signal.title)}</h3>${signal.evidence.map(item => {
    const row = responses.find(response => response.eventId === item.eventId);
    return `<p class="evidence">${E(row ? `${srcName[row.source]}, месяц ${row.month}` : 'Источник недоступен')} · ${E(S.schema.labels[item.field] || item.field)}: <strong>${E(item.value)}</strong></p>`;
  }).join('')}</article>`;
}
function selectedSignals(profileData) {
  const {responses, ruleSignals, ai, month} = profileData;
  const aiSignals = ai?.signals.filter(signal => signal.evidence.some(item => responses.some(row => row.eventId === item.eventId && row.month === month))) || [];
  return rankSignals([...ruleSignals, ...aiSignals]);
}
function summarySignals(signals) {
  const lowScores = new Set(signals.filter(signal => signal.kind === 'low_score')
    .flatMap(signal => signal.evidence.map(item => `${item.eventId}#${item.field}`)));
  return signals.filter(signal => signal.kind !== 'decline'
    || !signal.evidence.some(item => lowScores.has(`${item.eventId}#${item.field}`)));
}
function signalContext(signal, data) {
  const rows = signal.evidence.flatMap(item => {
    const row = data.responses.find(response => response.eventId === item.eventId);
    return row ? [{item, row, isScore: row.scores[item.field] != null}] : [];
  });
  const current = rows.find(({row, isScore}) => row.month === data.month && isScore);
  const field = current?.item.field;
  const previous = current && responseAt(data.responses, current.row.source, current.row.month - 1);
  return {rows, current, field, previous, previousValue: previous?.scores[field]};
}
function signalSummary(signal, data) {
  const {current, field, previous, previousValue} = signalContext(signal, data);
  const compact = signal.kind === 'low_score' && current;
  return `<article class="signal-summary">
    <div><h3>${E(compact ? S.schema.labels[field] : signal.title)}</h3>
      <small>${compact ? `${surveyTitles[current.row.source]} · месяц ${data.month}` : badge(signal.severity, true)}</small></div>
    ${compact ? `<div><strong class="tone-${E(signal.severity)}">${E(current.item.value)} из 5</strong>${previousValue == null ? '' : `<small>${previousValue} из 5 в месяце ${previous.month}</small>`}</div>` : ''}
    <button type="button" class="link-button" data-evidence="${E(signal.id)}">${icon('right')}Посмотреть основания</button>
  </article>`;
}
function openEvidence(id, trigger) {
  const data = S.profile;
  const signal = selectedSignals(data).find(item => item.id === id);
  if (!signal || document.querySelector('.evidence-drawer[open]')) return;
  const {rows, current, field, previous, previousValue} = signalContext(signal, data);
  const compact = signal.kind === 'low_score' && current;
  const scores = rows.filter(row => row.isScore);
  if (compact && previousValue != null && !scores.some(({item}) => item.eventId === previous.eventId && item.field === field)) {
    scores.push({row: previous, item: {field, value: previousValue}});
  }
  const comments = rows.filter(row => !row.isScore);
  const sameSurvey = compact && field === 'workload' && current.row.comments.difficulties;
  const summary = compact && previousValue != null
    ? `${S.schema.labels[field]}: ${previousValue} из 5 в месяце ${previous.month}, ${current.item.value} из 5 в месяце ${data.month}.`
    : signal.title;
  const drawer = document.createElement('dialog');
  drawer.className = 'evidence-drawer';
  drawer.setAttribute('aria-labelledby', 'evidence-heading');
  drawer.innerHTML = `<header class="drawer-head">
    <small>${E(data.person.id)} · Месяц ${data.month}</small>
    <form method="dialog"><button class="subtle-button" autofocus>Закрыть</button></form>
  </header>
  <h2 id="evidence-heading">${E(compact ? S.schema.labels[field] : signal.title)}</h2>
  ${badge(signal.severity, true)}
  <section class="drawer-summary"><h3>Что изменилось</h3><p>${E(summary)}</p></section>
  ${scores.length ? `<section><h3>Исходные оценки</h3><dl class="evidence-scores">
    ${scores.map(({row, item}) => `<div><dt><strong>Месяц ${row.month}</strong><small>${srcName[row.source]} · ${E(S.schema.labels[item.field] || item.field)}</small></dt><dd>${E(item.value)} из 5</dd></div>`).join('')}
  </dl></section>` : ''}
  ${comments.map(({row, item}) => `<section><h3>Комментарий · ${srcName[row.source]}</h3><small>Месяц ${row.month} · ${E(S.schema.labels[item.field] || item.field)}</small><blockquote>${E(item.value)}</blockquote></section>`).join('')}
  ${sameSurvey && !comments.length ? `<section><h3>Комментарий из той же анкеты</h3><blockquote>${E(sameSurvey)}</blockquote></section>` : ''}
  ${!rows.length ? '<p class="muted">Основания недоступны.</p>' : ''}
  ${S.who.role === 'hr' ? `<footer><section><h3>Что можно уточнить</h3><p>${field === 'workload' ? 'Обсудить со стажёром объём задач, сроки и приоритеты.' : 'Проверить исходные ответы и обсудить с участниками, какая поддержка нужна.'}</p></section><button type="button" class="subtle-button" data-nav="actions" data-action-signal="${E(signal.id)}">${icon('right')}Действия HR</button></footer>` : ''}`;
  drawer.addEventListener('close', () => {
    drawer.remove();
    if (trigger.isConnected) trigger.focus();
  });
  root.append(drawer);
  drawer.showModal();
}
const actionLabels={
  weekly_1on1:'Короткие еженедельные встречи',reduce_load:'Пересмотреть нагрузку',
  clarify_tasks:'Уточнить задачи',hr_talk:'Обсудить ситуацию с HR',other:'Согласовать следующий шаг'
};
function conversationPlanHTML(plan,rs){
  const evidence=plan.evidenceIds.map(id=>{
    const sep=id.lastIndexOf('#'),field=id.slice(sep+1),row=rs.find(r=>r.eventId===id.slice(0,sep));
    if(!row)return '';
    const value=row.scores[field]??row.comments[field];
    return `<li>${E(srcName[row.source])} · ${E(S.schema.labels[field]||field)}: ${E(value)}</li>`;
  }).join('');
  return `<section class="panel"><h2>План разговора 1:1 · месяц ${E(plan.month)}</h2>
    <p>${E(plan.situation)}</p><h3>Основания</h3><ul>${evidence}</ul>
    <h3>Вопросы для разговора</h3><ol>${plan.questions.map(q=>`<li>${E(q)}</li>`).join('')}</ol>
    <h3>Возможное действие</h3><p>${E(actionLabels[plan.actionType])}</p>
    <h3>Черновик сообщения наставнику</h3><p class="notice">${E(plan.mentorMessage)}</p></section>`;
}
function hrAnalysisHTML(d,rs){
  const stateText={not_connected:'Python-агент пока не подключен.',not_requested:'Анализ еще не запрашивался.',
    pending:'Запрос на анализ находится в очереди.',running:'Агент анализирует историю.',
    failed:'Анализ не выполнен. Запрос можно повторить в разделе интеграций.',
    superseded:'История изменилась, ожидается новый анализ.'};
  const ai=d.ai;
  const signals=ai?.signals.filter(s=>s.evidence.some(e=>rs.some(r=>r.eventId===e.eventId&&r.month===d.month)))||[];
  const plan=ai?.conversationPlans?.find(p=>p.month===d.month);
  return `<section class="panel"><div class="panelhead"><h2>Анализ комментариев · месяц ${E(d.month)}</h2>
    <button class="secondary" data-act="analyze">Запросить анализ</button></div>
    ${ai?signals.map(s=>signalHTML(s,rs)).join('')||'<p class="empty">За этот месяц нет подтвержденных сигналов по комментариям.</p>'
        :`<p class="notice">${E(stateText[d.aiState]||d.aiState)}</p>`}
    ${ai?.recommendations.length?`<h3>Возможные действия</h3><ul>${ai.recommendations.map(v=>`<li>${E(v)}</li>`).join('')}</ul>`:''}
    </section>${plan?conversationPlanHTML(plan,rs):''}`;
}
function recommendationsHTML(data, role) {
  if (role === 'intern') return '';
  const hr = role === 'hr';
  const title = hr ? 'Рекомендации по стажёру' : 'Рекомендации наставнику';
  const cards = (data.recommendations || []).map(item => {
    const delivery = item.delivery;
    let task = '';
    if (hr) {
      if (delivery?.state === 'sent') {
        task = `<p class="muted">Задача создана. ${safeLink(delivery.externalUrl, 'Открыть задачу')}</p>`;
      } else if (delivery?.state === 'sending') {
        task = '<p class="muted" role="status">Задача отправляется в HR-сервис.</p>';
      } else if (data.hrTaskConfigured) {
        const retry = delivery?.state === 'failed';
        task = `${retry ? `<p class="error" role="alert">${E(delivery.error)}</p>` : ''}
          <button type="button" class="secondary" data-hr-task="${E(item.id)}">${retry ? 'Повторить отправку' : 'Подтвердить и создать задачу'}</button>`;
      }
    }
    return `<article class="feedback"><h3>${E(item.title)}</h3>
      ${hr ? `<small>Получатель · ${item.audience === 'mentor' ? 'Наставник' : 'HR'}</small>` : ''}
      <p>${E(item.reason)}</p><ul>${item.questions.map(question => `<li>${E(question)}</li>`).join('')}</ul>${task}</article>`;
  }).join('');
  return `<section class="detail-section"><h2 class="section-heading">${title} · месяц ${E(data.month)}</h2>
    ${cards || '<p class="muted">За выбранный месяц рекомендаций пока нет.</p>'}
    ${hr && !data.hrTaskConfigured && cards ? '<p class="muted">Отправка задач появится после настройки HR-сервиса.</p>' : ''}</section>`;
}
function profile() {
  const data = S.profile;
  const {responses, month, person} = data;
  const role = S.who.role;
  const tabs = [['history', 'История и оценки'], ['comments', 'Комментарии'], ['feedback', 'Обратная связь']];
  const signals = selectedSignals(data);
  const sources = ['intern', 'mentor'].map(source => responseAt(responses, source, month));
  const complete = sources.every(row => row && Object.values(row.scores).every(value => value != null));
  const formStatus = !sources.some(Boolean) ? 'Нет анкет за месяц' : complete ? 'Обе анкеты получены'
    : sources.every(Boolean) ? 'Обе анкеты получены, есть пропуски' : 'Получена одна из двух анкет';
  let body;
  if (S.tab === 'comments') {
    body = `<section class="detail-section">
      <h2 class="section-heading">Комментарии за месяц ${month}</h2>
      <div class="comment-grid">${sources.map((row, index) => {
        const source = index === 0 ? 'intern' : 'mentor';
        const relevant = signals.find(signal => signal.evidence.some(item => row && item.eventId === row.eventId));
        return `<article class="feedback">
          <div class="panelhead"><h3>${surveyTitles[source]}</h3>${relevant ? `<button type="button" class="subtle-button" data-evidence="${E(relevant.id)}">${icon('right')}Посмотреть основания</button>` : ''}</div>
          ${row && role === 'mentor' && source === 'intern' ? '<p class="muted">Исходные комментарии стажёра доступны HR.</p>'
            : row ? Object.entries(row.comments).map(([key, value]) => `<div class="comment-entry"><small>${E(S.schema.labels[key] || key)}</small><p>${E(value || 'Ответ не заполнен')}</p></div>`).join('') : '<p class="muted">Анкета за этот месяц ещё не получена.</p>'}
        </article>`;
      }).join('')}</div>
    </section>`;
  } else if (S.tab === 'feedback') {
    body = `<section class="detail-section"><h2 class="section-heading">Обратная связь наставника</h2>
      ${data.feedback.map(item => `<article class="feedback"><h3>Месяц ${item.month} · ${E(item.mentorId)}</h3><small>${date(item.publishedAt)}</small><p>${E(item.text)}</p></article>`).join('') || '<div class="empty"><h3>Сообщений пока нет</h3><p>Здесь появятся рекомендации, опубликованные наставником для стажёра.</p></div>'}
      ${role === 'mentor' ? `<form id="feedback" class="detail-form"><h3>Опубликовать обратную связь</h3><label>Месяц<select name="month">${options(MONTHS.map(value => [value, `Месяц ${value}`]), month)}</select></label><label>Что получилось и какой следующий шаг<textarea name="text" required maxlength="5000"></textarea></label><div><button type="submit">Опубликовать для стажёра</button></div></form>` : ''}</section>`;
  } else {
    const timeline = source => `<section class="detail-section"><h3>${source === 'intern' ? 'Оценки стажёра' : 'Оценки наставника'}</h3>
      <div class="tablewrap" tabindex="0" role="region" aria-label="${surveyTitles[source]} по месяцам">
        <table class="scoretable"><thead><tr><th scope="col">Показатель</th>${MONTHS.map(value => `<th scope="col">М${value}</th>`).join('')}</tr></thead>
        <tbody>${S.schema[source + 'Fields'].map(field => `<tr><th scope="row">${E(S.schema.labels[field])}</th>${MONTHS.map(value => `<td>${score(responseAt(responses, source, value)?.scores[field])}</td>`).join('')}</tr>`).join('')}</tbody></table>
      </div></section>`;
    body = `${recommendationsHTML(data, role)}<section class="detail-section signal-list"><h2 class="signal-list-title">Сигналы за месяц ${month}</h2>
      ${summarySignals(signals).map(signal => signalSummary(signal, data)).join('') || `<p class="muted">${data.status === 'missing' ? 'Для анализа пока недостаточно данных.' : 'За этот месяц активных сигналов нет.'}</p>`}</section>
      ${chart(responses)}
      <details class="history-extra"><summary>Все показатели анкет</summary>${timeline('intern')}${timeline('mentor')}</details>
      ${role === 'hr' ? hrAnalysisHTML(data, responses) : ''}`;
  }
  shell(`<div class="profile">
    ${role === 'intern' ? '' : `<nav class="breadcrumbs" aria-label="Хлебные крошки"><button type="button" class="link-button" data-act="back">${role === 'hr' ? 'Обзор стажировок' : 'Мои стажёры'}</button>${icon('right')}<span aria-current="page">${E(person.id)}</span></nav>`}
    <header class="profile-head">
      <div><p class="eyebrow">${E(person.department)} · наставник ${E(person.mentorId)}</p><h1>${role === 'intern' ? 'Моя стажировка' : `Стажёр ${E(person.id)}`}</h1>
        <div class="profile-status">${badge(data.status, true)}<span>${formStatus}</span></div></div>
      <div class="controls">${monthSelect()}${role === 'mentor' ? '<button type="button" data-nav="survey">Заполнить анкету</button>' : ''}</div>
    </header>
    <div class="tabs-row">
      <div class="tabs" role="tablist" aria-label="Разделы профиля">${tabs.map(([id, label]) => `<button type="button" role="tab" id="tab-${id}" aria-controls="profile-tab" aria-selected="${S.tab === id}" tabindex="${S.tab === id ? '0' : '-1'}" data-tab="${id}">${label}</button>`).join('')}</div>
      ${role === 'intern' ? '<button type="button" class="link-button" data-nav="survey">Заполнить анкету</button>' : ''}
    </div>
    <div id="profile-tab" role="tabpanel" aria-labelledby="tab-${S.tab}">${body}</div>
  </div>`);
}
function survey() {
  const data = S.profile;
  const source = S.who.role;
  const {person, responses, month} = data;
  const fields = S.schema[source + 'Fields'];
  const commentFields = source === 'intern' ? ['difficulties', 'barriers'] : ['feedback', 'attention'];
  const existing = responseAt(responses, source, month);
  const external = source === 'intern' ? S.schema.internFormUrl : S.schema.mentorFormUrl;
  const result = S.surveyResult?.person === person.id && S.surveyResult?.month === month ? S.surveyResult : null;
  const heading = head('Ежемесячная анкета', `${person.id} · ${person.department} · ${surveyTitles[source].toLowerCase()}`);
  const externalLink = safeLink(external, 'Перейти к внешней форме');
  const externalPanel = externalLink ? `<aside class="notice"><strong>Доступна внешняя форма</strong><p>Ответ из неё появится в истории после синхронизации.</p>${externalLink}</aside>` : '';
  if (existing) {
    shell(`${heading}<section class="panel role-panel survey-result">
      <h2>${result ? 'Ответ за период сохранён' : 'Анкета уже получена'}</h2>
      <p>Месяц ${month} · ${surveyTitles[source]}. Ответ доступен в истории стажировки.</p>
      <button type="button" class="secondary" data-person="${E(person.id)}">Посмотреть историю ${icon('right')}</button>
    </section>`);
    return;
  }
  const scaleHelp = source === 'intern'
    ? '1 — совсем не согласен, 5 — полностью согласен. 2–4 — промежуточные значения.'
    : 'Оцените каждый показатель от 1 до 5. Оценки 2 и 4 — промежуточные.';
  shell(`${heading}${externalPanel}<form id="survey" class="survey-form">
    <div id="survey-error" role="alert"></div>
    <section class="panel role-panel"><h2>Оценки</h2><p class="muted">${scaleHelp}</p>
      ${fields.map(field => {
        const question = S.questions[field];
        return `<fieldset class="rating-row">
          <legend>${E(S.schema.labels[field])}</legend>
          <div class="rating-layout">
            <div>
              <p class="muted">${E(question?.question || '')}</p>
              ${question?.anchors ? `<ul class="rating-anchors">${Object.entries(question.anchors).map(([value, description]) => `<li><b>${E(value)}</b> — ${E(description)}</li>`).join('')}</ul>` : ''}
            </div>
            <div class="ratings">${[1, 2, 3, 4, 5].map(value => `<label><input type="radio" name="${E(field)}" value="${value}"><span>${value}</span></label>`).join('')}
              <button type="button" class="link-button clear-rating" data-clear-rating="${E(field)}" aria-label="Снять оценку: ${E(S.schema.labels[field])}" hidden>Снять оценку</button></div>
          </div>
        </fieldset>`;
      }).join('')}
    </section>
    <section class="panel role-panel survey-comments"><h2>Комментарии</h2>
      ${commentFields.map(field => `<label><strong>${E(S.schema.labels[field])}</strong><small>${E(S.questions[field]?.question || '')}</small><textarea name="${E(field)}" maxlength="5000" placeholder="Ваш ответ"></textarea></label>`).join('')}
    </section>
    <div class="survey-actions"><p class="muted" id="survey-progress">Оценок: 0 из ${fields.length}. Пустые оценки сохраняются как пропуски.</p><button type="submit">Отправить ответ</button></div>
  </form>`);
}
function updateSurveyProgress(form) {
  const selected = [...form.querySelectorAll('input:checked')];
  form.querySelector('#survey-progress').textContent = `Оценок: ${selected.length} из ${S.schema[S.who.role + 'Fields'].length}. Пустые оценки сохраняются как пропуски.`;
  for (const button of form.querySelectorAll('[data-clear-rating]')) {
    button.hidden = !selected.some(input => input.name === button.dataset.clearRating);
  }
}
async function submitSurvey(values) {
  const {person} = S.profile;
  const source = S.who.role;
  const month = S.month;
  const key = `mgts-event:${S.who.username}:${person.id}:${month}:${source}`;
  let eventId = sessionStorage.getItem(key);
  if (!eventId) {
    eventId = 'portal:' + crypto.randomUUID();
    sessionStorage.setItem(key, eventId);
  }
  const scores = Object.fromEntries(S.schema[source + 'Fields'].map(field => [field, values[field] ? Number(values[field]) : null]));
  const fields = source === 'intern' ? ['difficulties', 'barriers'] : ['feedback', 'attention'];
  const comments = Object.fromEntries(fields.map(field => [field, values[field] || '']));
  const row = {eventId, programId: S.schema.programId, internId: person.id, department: person.department, month, source, scores, comments};
  if (source === 'mentor') row.mentorId = S.who.subject;
  await api('/responses', 'POST', row);
  S.surveyResult = {person: person.id, month};
  if (S.view === 'survey' && S.person === person.id && S.month === month) await render();
}
function integrations(data) {
  const services = [
    ['Формы Tally', 'Анкеты стажёра и наставника', data.tallyConfigured, 'Не настроены'],
    ['Прямой webhook', 'Приём ответов от внешних систем', data.webhookConfigured, 'Не настроен'],
    ['Python-агент', 'Обработка текстовой обратной связи', data.agentConfigured, 'Не подключён'],
    ['HR-сервис задач', 'Передача рекомендаций после подтверждения HR', data.hrTaskConfigured, 'Не настроен'],
  ];
  const lastAccepted = data.deliveries.find(delivery => delivery.status === 'accepted');
  shell(`${detailHead('Интеграции', refreshButton())}
    <dl class="service-list">${services.map(([name, description, configured, missing]) => `<div>
      <dt><strong>${name}</strong><small>${description}</small></dt>
      <dd class="survey-status ${configured ? 'positive' : 'missing'}">${icon(configured ? 'positive' : 'missing')}${configured ? 'Настроен' : missing}</dd>
    </div>`).join('')}</dl>
    <section class="detail-section"><div class="panelhead"><h2>Последние доставки</h2><small>Последняя успешная доставка: ${lastAccepted ? date(lastAccepted.receivedAt) : '—'}</small></div>
      ${data.deliveries.length ? `<div class="tablewrap" role="region" aria-label="Последние доставки" tabindex="0"><table><thead><tr><th>Время</th><th>Источник</th><th>Событие</th><th>Результат</th></tr></thead>
      <tbody>${data.deliveries.map(item => `<tr><td class="nowrap">${date(item.receivedAt)}</td><td>${E(item.source)}</td><td>${E(item.eventId)}</td><td>${E(item.status)}</td></tr>`).join('')}</tbody></table></div>` : '<p class="muted">Доставок пока нет.</p>'}
    </section>
    <section class="detail-section"><h2>Очередь анализа</h2>
      ${data.jobs.length ? `<div class="tablewrap" role="region" aria-label="Очередь анализа" tabindex="0"><table><thead><tr><th>Стажёр</th><th>Состояние</th><th>Попыток</th><th>Последняя ошибка</th><th>Действие</th></tr></thead>
      <tbody>${data.jobs.map(job => `<tr><td>${E(job.internId)}</td><td>${E(job.state)}</td><td>${job.attempts}</td><td>${E(job.lastError || '—')}</td><td>${job.state === 'failed' ? `<button type="button" class="secondary" data-retry="${E(job.id)}">Повторить</button>` : '—'}</td></tr>`).join('')}</tbody></table></div>` : '<p class="muted">Задач на анализ пока нет.</p>'}
    </section>
    <a class="export-link" href="/api/v1/hr/export" download>Выгрузить исходные ответы JSON ${icon('right')}</a>`);
}
function actions(list) {
  shell(`${detailHead('Действия HR')}
    <section class="detail-section action-editor"><h2>Новая мера поддержки</h2>
      ${S.actionContext ? `<div class="notice action-context"><strong>Основание · месяц ${S.actionContext.month}</strong><p>${E(S.actionContext.title)}</p></div>` : ''}
      <form id="action" class="detail-form">
        <label class="form-select">Стажёр<select name="internId" required>${options(S.schema.people.map(person => [person.id, `${person.id} · ${person.department}`]), S.person)}</select></label>
        <label>Действие и ожидаемый результат<textarea name="note" maxlength="3000" required placeholder="Опишите, что нужно сделать"></textarea></label>
        <div><button type="submit"${S.schema.people.length ? '' : ' disabled'}>Зарегистрировать действие</button></div>
      </form>
    </section>
    <section class="detail-section action-journal"><h2>Журнал действий</h2>
      ${list.map(item => `<article class="actionitem"><div><h3>${E(item.internId)} · ${item.status === 'closed' ? 'Закрыто' : 'В работе'}</h3>
        <small>${E(item.owner)} · ${date(item.createdAt)}${item.closedAt ? ` · Закрыто ${date(item.closedAt)}` : ''}</small><p>${E(item.note)}</p></div>
        ${item.status === 'open' ? `<button type="button" class="secondary" data-close="${E(item.id)}">Закрыть действие</button>` : ''}</article>`).join('') || '<div class="empty"><h3>Действий пока нет</h3><p>Здесь появятся зарегистрированные меры поддержки.</p></div>'}
    </section>`);
}
function confirmActionClosure(id, trigger) {
  const dialog = document.createElement('dialog');
  dialog.className = 'confirm-dialog';
  dialog.setAttribute('aria-labelledby', 'close-heading');
  dialog.innerHTML = `<h2 id="close-heading">Закрыть меру поддержки?</h2><p>Она останется в журнале со статусом «Закрыто».</p><div class="confirm-error" role="alert"></div>
    <form method="dialog" class="confirm-actions"><button type="submit" class="secondary" autofocus>Отмена</button><button type="button" data-confirm-close="${E(id)}">Закрыть меру</button></form>`;
  dialog.addEventListener('close', () => {
    dialog.remove();
    if (trigger.isConnected) trigger.focus();
  });
  root.append(dialog);
  dialog.showModal();
}
function enroll() {
  shell(`${detailHead('Новый участник')}
    <section class="detail-section enrollment"><form id="enroll" class="detail-form">
      <label>ID стажёра<input name="id" required maxlength="50" placeholder="СТ-006"></label>
      <label>Подразделение<input name="department" required maxlength="100" placeholder="Например, IT"></label>
      <label>ID наставника<input name="mentorId" required maxlength="50" placeholder="Н-006"></label>
      <small>Учётные записи создаёт администратор.</small>
      <div><button type="submit">Зарегистрировать участника</button></div>
    </form></section>`);
}
function selectProfileTab(tab) {
  S.tab = tab;
  profile();
  document.querySelector('#tab-' + tab)?.focus({preventScroll: true});
}
document.addEventListener('click', async event => {
  const button = event.target.closest('button');
  if (!button) return;
  const {nav, person, tab, retry, close, confirmClose, act, evidence, period, openProfile, clearRating, actionSignal, hrTask} = button.dataset;
  try {
    if (period) {
      S.month = Number(period);
      await render();
      document.querySelector('[data-period][aria-current="true"]')?.focus();
    } else if (openProfile) {
      S.tab = openProfile;
      S.view = 'profile';
      await render();
      window.scrollTo(0, 0);
      document.querySelector('#main')?.focus();
    } else if (clearRating) {
      const form = button.form || button.closest('form');
      const input = [...form.querySelectorAll('input:checked')].find(control => control.name === clearRating);
      if (input) {
        input.checked = false;
        updateSurveyProgress(form);
        input.focus();
      }
    } else if (evidence) {
      openEvidence(evidence, button);
    } else if (nav || person || act === 'back') {
      if (nav === 'actions') {
        const signal = actionSignal && selectedSignals(S.profile).find(item => item.id === actionSignal);
        S.actionContext = signal ? {month: S.month, title: signal.title} : null;
      }
      button.closest('dialog')?.close();
      if (person) S.person = person;
      S.tab = 'history';
      S.view = nav || (person ? 'profile' : 'overview');
      const generation = S.renderGeneration + 1;
      await render();
      if (S.renderGeneration === generation && S.who) {
        window.scrollTo(0, 0);
        document.querySelector('#main')?.focus({preventScroll: true});
      }
    } else if (tab) {
      selectProfileTab(tab);
    } else if (retry) {
      await api('/hr/jobs/' + encodeURIComponent(retry) + '/retry', 'POST');
      toast('Запрос возвращён в очередь');
      await render();
    } else if (hrTask) {
      const id = S.person;
      const month = S.month;
      button.disabled = true;
      try {
        const delivery = await api('/hr/interns/' + encodeURIComponent(id) + '/recommendations/' + encodeURIComponent(hrTask) + '/confirm?month=' + month, 'POST');
        toast(delivery.state === 'sent' ? 'Задача создана' : 'Задача отправляется');
      } finally {
        if (S.view === 'profile' && S.person === id && S.month === month) await render();
        button.disabled = false;
      }
    } else if (close) {
      confirmActionClosure(close, button);
    } else if (confirmClose) {
      const dialog = button.closest('dialog');
      button.disabled = true;
      try {
        await api('/hr/actions/' + encodeURIComponent(confirmClose), 'PATCH', {status: 'closed'});
        dialog.close();
        toast('Мера поддержки закрыта');
        await render();
      } catch (error) {
        dialog.querySelector('.confirm-error').textContent = `Не удалось закрыть меру. ${error.message}`;
      } finally {
        button.disabled = false;
      }
    } else if (act === 'logout') {
      sessionStorage.removeItem('pulse:signed-in');
      await api('/auth/logout', 'POST');
      S.who = null;
      S.integrationSummary = null;
      loginPage();
    } else if (act === 'refresh') {
      await render(true);
    } else if (act === 'analyze') {
      await api('/hr/interns/' + encodeURIComponent(S.person) + '/analysis', 'POST');
      toast('Анализ запрошен');
      await render();
    }
  } catch (error) {
    toast(error.message);
  }
});
document.addEventListener('keydown', event => {
  const tab = event.target.closest('[role="tab"]');
  if (!tab || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
  const tabs = [...tab.parentElement.querySelectorAll('[role="tab"]')];
  const current = tabs.indexOf(tab);
  const next = {ArrowLeft: (current + tabs.length - 1) % tabs.length, ArrowRight: (current + 1) % tabs.length, Home: 0, End: tabs.length - 1};
  event.preventDefault();
  selectProfileTab(tabs[next[event.key]].dataset.tab);
});
document.addEventListener('change', async event => {
  if (event.target.id === 'month') {
    S.month = Number(event.target.value);
    document.querySelector('#main')?.setAttribute('aria-busy', 'true');
    const generation = S.renderGeneration + 1;
    await render();
    if (S.renderGeneration === generation) document.querySelector('#month')?.focus({preventScroll: true});
  } else if (event.target.id === 'metric') {
    S.metric = event.target.value;
    profile();
    document.querySelector('#metric')?.focus({preventScroll: true});
  } else {
    const filter = {'department-filter': 'department', 'status-filter': 'statusFilter', 'sort-filter': 'sort'}[event.target.id];
    if (!filter) return;
    S[filter] = event.target.value;
    document.querySelector('#queue-results').innerHTML = overviewResults();
  }
});
document.addEventListener('input', event => {
  if (event.target instanceof HTMLInputElement && event.target.type === 'radio' && event.target.form?.getAttribute('id') === 'survey') {
    updateSurveyProgress(event.target.form);
  } else if (event.target.id === 'search') {
    S.search = event.target.value;
    document.querySelector('#queue-results').innerHTML = overviewResults();
  }
});
document.addEventListener('submit', async event => {
  const form = event.target;
  if (form.method === 'dialog') return;
  event.preventDefault();
  const formId = form.getAttribute('id');
  const button = form.querySelector('button[type="submit"]');
  const values = Object.fromEntries(new FormData(form));
  const initialLabel = button?.textContent;
  if (button) button.disabled = true;
  form.setAttribute('aria-busy', 'true');
  try {
    if (formId === 'login') {
      form.querySelector('#login-error').replaceChildren();
      button.textContent = 'Проверяем…';
      S.who = await api('/auth/login', 'POST', values);
      await loadSession();
    } else if (formId === 'survey') {
      form.querySelector('#survey-error').replaceChildren();
      button.textContent = 'Отправляем…';
      await submitSurvey(values);
    } else if (formId === 'feedback') {
      await api('/mentor/interns/' + encodeURIComponent(S.person) + '/feedback', 'POST', {month: Number(values.month), text: values.text});
      toast('Обратная связь опубликована');
      S.composingFeedback = false;
      await render();
    } else if (formId === 'enroll') {
      await api('/hr/enrollments', 'POST', values);
      S.schema = await api('/schema');
      toast('Участник зарегистрирован');
      S.view = 'overview';
      await render();
    } else if (formId === 'action') {
      await api('/hr/actions', 'POST', values);
      toast('Мера поддержки зарегистрирована');
      S.actionContext = null;
      await render();
    }
  } catch (error) {
    if (formId === 'login' && form.isConnected) {
      const title = error.status === 401 ? 'Неверный логин или пароль' : 'Не удалось выполнить вход';
      form.querySelector('#login-error').innerHTML = `<p class="error"><strong>${title}</strong><br>${E(error.message)}</p>`;
    } else if (formId === 'survey' && form.isConnected) {
      const title = error.status === 409 ? 'Анкета за этот период уже получена' : error.status === 422 ? 'Проверьте ответы в анкете' : 'Не удалось отправить ответ';
      form.querySelector('#survey-error').innerHTML = `<div class="error"><p><strong>${title}</strong><br>${E(error.message)}</p>
        ${error.status === 409 ? '<button type="button" class="secondary" data-act="refresh">Проверить сохранённый ответ</button>' : ''}</div>`;
      form.querySelector('#survey-error').scrollIntoView({block: 'nearest'});
    } else if (form.isConnected) {
      toast(error.message);
    }
  } finally {
    form.removeAttribute('aria-busy');
    if (button) {
      button.disabled = false;
      button.textContent = initialLabel;
    }
  }
});
// Refresh read-only screens after external deliveries, without interrupting forms.
setInterval(async()=>{if(!S.who||document.hidden||S.refreshing||document.querySelector('dialog[open]'))return;if(document.activeElement?.matches('button, a, input, select, textarea, summary, [tabindex=\"0\"]'))return;if(!((S.view==='overview')||(S.view==='profile'&&S.tab==='history')))return;S.refreshing=true;try{await render()}finally{S.refreshing=false}},15000);
init();
