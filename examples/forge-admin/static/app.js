const loginView = document.getElementById('login-view');
const dashboardView = document.getElementById('dashboard-view');
const loginForm = document.getElementById('login-form');
const loginError = document.getElementById('login-error');
const reposTable = document.getElementById('repos-table');
const appsTable = document.getElementById('apps-table');
const deploymentsTable = document.getElementById('deployments-table');
const deployForm = document.getElementById('deploy-form');
const deployRepo = document.getElementById('deploy-repo');
const deployResult = document.getElementById('deploy-result');
const appPanel = document.getElementById('app-panel');
const appTitle = document.getElementById('app-title');
const appMeta = document.getElementById('app-meta');
const secretForm = document.getElementById('secret-form');
const secretsTable = document.getElementById('secrets-table');
const deploymentPanel = document.getElementById('deployment-panel');
const deploymentTitle = document.getElementById('deployment-title');
const deploymentMeta = document.getElementById('deployment-meta');
const deploymentActions = document.getElementById('deployment-actions');
const logsOutput = document.getElementById('logs-output');
const confirmDialog = document.getElementById('confirm-dialog');
const confirmTitle = document.getElementById('confirm-title');
const confirmMessage = document.getElementById('confirm-message');

const state = {
  repos: [],
  workers: [],
  apps: [],
  deployments: [],
  selectedApp: null,
  selectedDeployment: null,
  logs: new Map(),
  events: null,
};

function setText(id, value) {
  document.getElementById(id).textContent = String(value);
}

function clearNode(node) {
  while (node.firstChild) {
    node.removeChild(node.firstChild);
  }
}

function text(value) {
  return value == null || value === '' ? '-' : String(value);
}

function shortSHA(value) {
  return value ? value.slice(0, 12) : '-';
}

function repoFromURL(url) {
  const match = /^https:\/\/github\.com\/([^/]+\/[^/.]+)(?:\.git)?$/.exec(url || '');
  return match ? match[1] : '';
}

async function api(path, options = {}) {
  const response = await fetch(path, {...options, cache: 'no-store'});
  if (response.status === 401) {
    showLogin();
    throw new Error('not authenticated');
  }
  const body = await response.text();
  const data = body ? JSON.parse(body) : {};
  if (!response.ok) {
    throw new Error(data.error || `request failed: ${response.status}`);
  }
  return data;
}

function showLogin() {
  loginView.hidden = false;
  dashboardView.hidden = true;
  closeEvents();
}

function showDashboard() {
  loginView.hidden = true;
  dashboardView.hidden = false;
  openEvents();
}

function td(row, value) {
  const cell = document.createElement('td');
  cell.textContent = text(value);
  row.appendChild(cell);
  return cell;
}

function button(label, action, payload, danger = false) {
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.textContent = label;
  btn.dataset.action = action;
  Object.entries(payload || {}).forEach(([key, value]) => {
    btn.dataset[key] = String(value);
  });
  if (danger) {
    btn.className = 'danger';
  }
  return btn;
}

function actionCell(row, buttons) {
  const cell = document.createElement('td');
  const group = document.createElement('div');
  group.className = 'actions';
  buttons.filter(Boolean).forEach((btn) => group.appendChild(btn));
  cell.appendChild(group);
  row.appendChild(cell);
}

function renderRepos() {
  clearNode(reposTable);
  clearNode(deployRepo);
  state.repos.forEach((repo) => {
    const option = document.createElement('option');
    option.value = repo.repo;
    option.textContent = repo.repo;
    deployRepo.appendChild(option);

    const row = document.createElement('tr');
    td(row, repo.repo);
    td(row, repo.has_credential ? 'configured' : 'missing');
    actionCell(row, [
      button('Set token', 'set-credential', {repo: repo.repo}),
      repo.has_credential ? button('Delete', 'delete-credential', {repo: repo.repo}, true) : null,
    ]);
    reposTable.appendChild(row);
  });
}

function renderApps() {
  clearNode(appsTable);
  state.apps.forEach((app) => {
    const row = document.createElement('tr');
    row.dataset.action = 'select-app';
    row.dataset.app = app.app_name;
    td(row, app.app_name);
    td(row, app.status);
    td(row, app.active_deployment_id || '-');
    td(row, app.health_status || app.health_reason || '-');
    const hostCell = td(row, '');
    if (app.host) {
      const link = document.createElement('a');
      link.href = `https://${app.host}`;
      link.target = '_blank';
      link.rel = 'noopener';
      link.textContent = app.host;
      hostCell.appendChild(link);
    } else {
      hostCell.textContent = '-';
    }
    actionCell(row, [
      button('Redeploy HEAD', 'redeploy-head', {deployment: app.id}),
      app.active_deployment_id ? button('Stop', 'stop-deployment', {deployment: app.active_deployment_id}, true) : null,
      button('Secrets', 'select-app', {app: app.app_name}),
    ]);
    appsTable.appendChild(row);
  });
}

function renderDeployments() {
  clearNode(deploymentsTable);
  let failed = 0;
  state.deployments.forEach((deployment) => {
    if (deployment.status === 'failed') {
      failed++;
    }
    const row = document.createElement('tr');
    row.dataset.action = 'select-deployment';
    row.dataset.deployment = deployment.id;
    td(row, deployment.id);
    td(row, deployment.app_name);
    td(row, deployment.status);
    td(row, deployment.branch);
    td(row, shortSHA(deployment.commit_sha));
    actionCell(row, deploymentButtons(deployment));
    deploymentsTable.appendChild(row);
  });
  setText('failed-count', failed);
}

function deploymentButtons(deployment) {
  const buttons = [button('Logs', 'select-deployment', {deployment: deployment.id})];
  if (deployment.status === 'failed' || deployment.status === 'stopped') {
    buttons.push(button('Retry', 'retry-deployment', {deployment: deployment.id}));
  }
  if (deployment.status === 'pending' || deployment.status === 'building' || deployment.status === 'deploying') {
    buttons.push(button('Cancel', 'stop-deployment', {deployment: deployment.id}, true));
  }
  if (deployment.status === 'running') {
    buttons.push(button('Stop', 'stop-deployment', {deployment: deployment.id}, true));
  }
  if (deployment.commit_sha) {
    buttons.push(button('Rollback', 'rollback-deployment', {deployment: deployment.id}));
  }
  return buttons;
}

function renderSelectedApp() {
  if (!state.selectedApp) {
    appPanel.hidden = true;
    return;
  }
  const app = state.apps.find((item) => item.app_name === state.selectedApp);
  if (!app) {
    appPanel.hidden = true;
    return;
  }
  appPanel.hidden = false;
  appTitle.textContent = app.app_name;
  renderDefinitionList(appMeta, {
    Status: app.status,
    Host: app.host,
    Health: app.health_reason || app.health_status || '-',
    'Latest deployment': app.id,
    'Active deployment': app.active_deployment_id || '-',
    Repo: repoFromURL(app.repo_url),
  });
  loadSecrets(app.app_name).catch(showError);
}

function renderSelectedDeployment() {
  if (!state.selectedDeployment) {
    deploymentPanel.hidden = true;
    return;
  }
  const deployment = state.deployments.find((item) => String(item.id) === String(state.selectedDeployment));
  if (!deployment) {
    deploymentPanel.hidden = true;
    return;
  }
  deploymentPanel.hidden = false;
  deploymentTitle.textContent = `Deployment ${deployment.id}`;
  renderDefinitionList(deploymentMeta, {
    App: deployment.app_name,
    Status: deployment.status,
    Branch: deployment.branch,
    Commit: deployment.commit_sha,
    Agent: deployment.assigned_agent_id || '-',
    Host: deployment.host,
    Port: deployment.target_port || '-',
    Health: deployment.health_reason || deployment.health_status || '-',
  });
  clearNode(deploymentActions);
  deploymentButtons(deployment).forEach((btn) => deploymentActions.appendChild(btn));
  loadLogs(deployment.id).catch(showError);
}

function renderDefinitionList(node, values) {
  clearNode(node);
  Object.entries(values).forEach(([key, value]) => {
    const dt = document.createElement('dt');
    dt.textContent = key;
    const dd = document.createElement('dd');
    dd.textContent = text(value);
    node.appendChild(dt);
    node.appendChild(dd);
  });
}

async function refresh() {
  const [repos, workers, apps, deployments] = await Promise.all([
    api('/api/v1/repos'),
    api('/api/v1/agents'),
    api('/api/v1/apps'),
    api('/api/v1/deployments'),
  ]);
  state.repos = repos.repos || [];
  state.workers = workers;
  state.apps = apps;
  state.deployments = deployments;
  setText('workers-count', workers.length);
  setText('apps-count', apps.length);
  setText('deployments-count', deployments.length);
  renderRepos();
  renderApps();
  renderDeployments();
  renderSelectedApp();
  renderSelectedDeployment();
  showDashboard();
}

async function loadSecrets(appName) {
  const data = await api(`/api/v1/apps/${encodeURIComponent(appName)}/secrets`);
  clearNode(secretsTable);
  (data.keys || []).forEach((key) => {
    const row = document.createElement('tr');
    td(row, key);
    actionCell(row, [button('Delete', 'delete-secret', {app: appName, key}, true)]);
    secretsTable.appendChild(row);
  });
}

async function loadLogs(id) {
  const data = await api(`/api/v1/deployments/${id}/logs?limit=300`);
  const lines = (data.events || []).map(formatLogLine);
  state.logs.set(String(id), lines);
  if (String(state.selectedDeployment) === String(id)) {
    logsOutput.textContent = lines.join('\n');
    logsOutput.scrollTop = logsOutput.scrollHeight;
  }
}

function formatLogLine(event) {
  return `[${event.created_at}] ${event.level}: ${event.message}`;
}

function appendLiveLog(event) {
  const id = String(event.deployment_id || '');
  if (!id) {
    return;
  }
  const lines = state.logs.get(id) || [];
  lines.push(formatLogLine(event));
  state.logs.set(id, lines.slice(-500));
  if (String(state.selectedDeployment) === id) {
    logsOutput.textContent = state.logs.get(id).join('\n');
    logsOutput.scrollTop = logsOutput.scrollHeight;
  }
}

function openEvents() {
  if (state.events || typeof EventSource === 'undefined') {
    return;
  }
  const events = new EventSource('/api/v1/events');
  events.addEventListener('open', () => setText('connection-state', 'Live'));
  events.addEventListener('error', () => setText('connection-state', 'Reconnecting'));
  events.addEventListener('log', (event) => appendLiveLog(JSON.parse(event.data)));
  events.addEventListener('deployment', () => refresh().catch(() => {}));
  state.events = events;
}

function closeEvents() {
  if (state.events) {
    state.events.close();
    state.events = null;
  }
}

function confirmAction(title, message) {
  confirmTitle.textContent = title;
  confirmMessage.textContent = message;
  if (!confirmDialog.showModal) {
    return Promise.resolve(window.confirm(`${title}\n\n${message}`));
  }
  confirmDialog.showModal();
  return new Promise((resolve) => {
    confirmDialog.addEventListener('close', () => resolve(confirmDialog.returnValue === 'ok'), {once: true});
  });
}

async function promptValue(label, secret = true) {
  const value = window.prompt(label);
  if (value == null || value === '') {
    return null;
  }
  return secret ? value : value.trim();
}

function showError(error) {
  deployResult.textContent = error.message || String(error);
}

async function runAction(action, data) {
  if (action === 'select-app') {
    state.selectedApp = data.app;
    renderSelectedApp();
    return;
  }
  if (action === 'select-deployment') {
    state.selectedDeployment = data.deployment;
    renderSelectedDeployment();
    return;
  }
  if (action === 'redeploy-head') {
    const deployment = state.deployments.find((item) => String(item.id) === String(data.deployment));
    const repo = repoFromURL(deployment && deployment.repo_url);
    if (!repo || !(await confirmAction('Redeploy HEAD', `Deploy latest ${deployment.branch} from ${repo}?`))) {
      return;
    }
    await api('/api/v1/deployments', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({repo, branch: deployment.branch}),
    });
  }
  if (action === 'retry-deployment' && await confirmAction('Retry deployment', `Retry deployment ${data.deployment}?`)) {
    await api(`/api/v1/deployments/${data.deployment}/retry`, {method: 'POST'});
  }
  if (action === 'rollback-deployment' && await confirmAction('Rollback deployment', `Deploy the commit from deployment ${data.deployment}?`)) {
    await api(`/api/v1/deployments/${data.deployment}/rollback`, {method: 'POST'});
  }
  if (action === 'stop-deployment' && await confirmAction('Stop or cancel deployment', `Stop or cancel deployment ${data.deployment}?`)) {
    await api(`/api/v1/deployments/${data.deployment}`, {method: 'DELETE'});
  }
  if (action === 'set-credential') {
    const token = await promptValue(`GitHub token for ${data.repo}`);
    if (token) {
      const [owner, name] = data.repo.split('/');
      await api(`/api/v1/repos/${owner}/${name}/credential`, {
        method: 'PUT',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({token}),
      });
    }
  }
  if (action === 'delete-credential' && await confirmAction('Delete credential', `Delete credential for ${data.repo}?`)) {
    const [owner, name] = data.repo.split('/');
    await api(`/api/v1/repos/${owner}/${name}/credential`, {method: 'DELETE'});
  }
  if (action === 'delete-secret' && await confirmAction('Delete secret', `Delete ${data.key} from ${data.app}?`)) {
    await api(`/api/v1/apps/${encodeURIComponent(data.app)}/secrets/${encodeURIComponent(data.key)}`, {method: 'DELETE'});
    await loadSecrets(data.app);
  }
  await refresh();
}

loginForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  loginError.textContent = '';
  const password = new FormData(loginForm).get('password');
  try {
    await api('/login', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({password}),
    });
    await refresh();
  } catch (error) {
    loginError.textContent = error.message === 'too many failed login attempts' ? error.message : 'Sign in failed';
  }
});

deployForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  deployResult.textContent = '';
  const form = new FormData(deployForm);
  try {
    const deployment = await api('/api/v1/deployments', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({
        repo: form.get('repo'),
        branch: form.get('branch'),
        commit_sha: form.get('commit_sha'),
      }),
    });
    deployResult.textContent = `Queued deployment ${deployment.id}`;
    await refresh();
  } catch (error) {
    showError(error);
  }
});

secretForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  if (!state.selectedApp) {
    return;
  }
  const form = new FormData(secretForm);
  await api(`/api/v1/apps/${encodeURIComponent(state.selectedApp)}/secrets/${encodeURIComponent(form.get('key'))}`, {
    method: 'PUT',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({value: form.get('value')}),
  });
  secretForm.reset();
  await loadSecrets(state.selectedApp);
});

document.addEventListener('click', (event) => {
  const target = event.target.closest('[data-action]');
  if (!target) {
    return;
  }
  event.preventDefault();
  event.stopPropagation();
  runAction(target.dataset.action, target.dataset).catch(showError);
});

document.getElementById('logout').addEventListener('click', async () => {
  await fetch('/logout', {method: 'POST', cache: 'no-store'});
  showLogin();
});

document.getElementById('refresh').addEventListener('click', () => refresh().catch(showError));
document.getElementById('close-app').addEventListener('click', () => {
  state.selectedApp = null;
  appPanel.hidden = true;
});
document.getElementById('close-deployment').addEventListener('click', () => {
  state.selectedDeployment = null;
  deploymentPanel.hidden = true;
});

refresh().catch(showLogin);
setInterval(() => {
  if (!dashboardView.hidden) {
    refresh().catch(() => {});
  }
}, 15000);
