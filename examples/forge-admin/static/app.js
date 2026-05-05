const loginView = document.getElementById('login-view');
const dashboardView = document.getElementById('dashboard-view');
const loginForm = document.getElementById('login-form');
const loginError = document.getElementById('login-error');
const workersTable = document.getElementById('workers-table');
const appsTable = document.getElementById('apps-table');
const deploymentsTable = document.getElementById('deployments-table');
const logsPanel = document.getElementById('logs-panel');
const logsTitle = document.getElementById('logs-title');
const logsOutput = document.getElementById('logs-output');

function setText(id, value) {
  document.getElementById(id).textContent = String(value);
}

function clearNode(node) {
  while (node.firstChild) {
    node.removeChild(node.firstChild);
  }
}

function cell(row, value) {
  const td = document.createElement('td');
  td.textContent = value == null || value === '' ? '-' : String(value);
  row.appendChild(td);
}

async function api(path, options = {}) {
  const response = await fetch(path, {...options, cache: 'no-store'});
  if (response.status === 401) {
    showLogin();
    throw new Error('not authenticated');
  }
  if (!response.ok) {
    throw new Error(`request failed: ${response.status}`);
  }
  return response.json();
}

function showLogin() {
  loginView.hidden = false;
  dashboardView.hidden = true;
}

function showDashboard() {
  loginView.hidden = true;
  dashboardView.hidden = false;
}

function renderWorkers(workers) {
  clearNode(workersTable);
  workers.forEach((worker) => {
    const row = document.createElement('tr');
    cell(row, worker.id);
    cell(row, worker.address);
    cell(row, worker.cpu_used);
    cell(row, worker.memory_used);
    workersTable.appendChild(row);
  });
}

function renderApps(apps) {
  clearNode(appsTable);
  apps.forEach((app) => {
    const row = document.createElement('tr');
    cell(row, app.app_name);
    cell(row, app.status);
    cell(row, app.active_deployment_id);
    cell(row, app.active_status);
    cell(row, app.host);
    appsTable.appendChild(row);
  });
}

function renderDeployments(deployments) {
  clearNode(deploymentsTable);
  deployments.forEach((deployment) => {
    const row = document.createElement('tr');
    row.dataset.deploymentId = deployment.id;
    cell(row, deployment.id);
    cell(row, deployment.app_name);
    cell(row, deployment.status);
    cell(row, deployment.branch);
    row.addEventListener('click', () => showLogs(deployment.id));
    deploymentsTable.appendChild(row);
  });
}

async function refresh() {
  const [workers, apps, deployments] = await Promise.all([
    api('/api/v1/agents'),
    api('/api/v1/apps'),
    api('/api/v1/deployments'),
  ]);
  setText('workers-count', workers.length);
  setText('apps-count', apps.length);
  setText('deployments-count', deployments.length);
  renderWorkers(workers);
  renderApps(apps);
  renderDeployments(deployments);
  showDashboard();
}

async function showLogs(id) {
  const data = await api(`/api/v1/deployments/${id}/logs`);
  logsTitle.textContent = `Logs for deployment ${id}`;
  logsOutput.textContent = (data.events || []).map((event) => {
    return `[${event.created_at}] ${event.level}: ${event.message}`;
  }).join('\n');
  logsPanel.hidden = false;
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
    loginError.textContent = 'Sign in failed';
  }
});

document.getElementById('logout').addEventListener('click', async () => {
  await fetch('/logout', {method: 'POST', cache: 'no-store'});
  showLogin();
});

document.getElementById('close-logs').addEventListener('click', () => {
  logsPanel.hidden = true;
  logsOutput.textContent = '';
});

refresh().catch(showLogin);
setInterval(() => {
  if (!dashboardView.hidden) {
    refresh().catch(() => {});
  }
}, 10000);
