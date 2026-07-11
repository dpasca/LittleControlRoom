(() => {
  "use strict";

  const state = {
    dashboard: null,
    category: window.localStorage.getItem("lcr.mobile.category") || "main",
    bucket: window.localStorage.getItem("lcr.mobile.bucket") || "attention",
    query: "",
    selectedPath: "",
    socket: null,
    reconnectTimer: 0,
    refreshTimer: 0,
    connection: "connecting",
  };

  const elements = {
    body: document.body,
    systemTime: document.getElementById("system-time"),
    refreshButton: document.getElementById("refresh-button"),
    connectionDot: document.getElementById("connection-dot"),
    connectionLabel: document.getElementById("connection-label"),
    operatorBay: document.getElementById("operator-bay"),
    operatorCount: document.getElementById("operator-count"),
    operatorLabel: document.getElementById("operator-label"),
    operatorCopy: document.getElementById("operator-copy"),
    operatorSprite: document.getElementById("operator-sprite"),
    operatorLamp: document.getElementById("operator-lamp"),
    operatorCaption: document.getElementById("operator-caption"),
    attentionCount: document.getElementById("attention-count"),
    activeCount: document.getElementById("active-count"),
    allCount: document.getElementById("all-count"),
    updatedLabel: document.getElementById("updated-label"),
    categoryTabs: document.getElementById("category-tabs"),
    bucketFilter: document.getElementById("bucket-filter"),
    search: document.getElementById("project-search"),
    queueTitle: document.getElementById("queue-title"),
    queueCount: document.getElementById("queue-count"),
    dashboardState: document.getElementById("dashboard-state"),
    projectList: document.getElementById("project-list"),
    detailView: document.getElementById("detail-view"),
    detailState: document.getElementById("detail-state"),
    detailContent: document.getElementById("detail-content"),
    detailCategory: document.getElementById("detail-category"),
    detailTitle: document.getElementById("detail-title"),
    detailSummary: document.getElementById("detail-summary"),
    detailBadges: document.getElementById("detail-badges"),
    detailBlocks: document.getElementById("detail-blocks"),
    backButton: document.getElementById("back-button"),
  };

  function createElement(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function toneClass(tone) {
    const allowed = new Set(["muted", "info", "positive", "warning", "danger", "conflict"]);
    return allowed.has(tone) ? `tone-${tone}` : "";
  }

  async function fetchJSON(url) {
    const response = await window.fetch(url, {
      headers: { Accept: "application/json" },
      cache: "no-store",
    });
    if (!response.ok) {
      const message = (await response.text()).trim();
      throw new Error(message || `Request failed (${response.status})`);
    }
    return response.json();
  }

  async function loadDashboard(showSpinner = true) {
    if (showSpinner) elements.refreshButton.classList.add("spinning");
    try {
      state.dashboard = await fetchJSON("/api/mobile/dashboard");
      ensureSelectedCategory();
      renderDashboard();
    } catch (error) {
      renderDashboardError(error);
      setConnection("offline", "Offline");
    } finally {
      elements.refreshButton.classList.remove("spinning");
    }
  }

  function ensureSelectedCategory() {
    const categories = state.dashboard?.categories || [];
    if (!categories.some((category) => category.id === state.category)) {
      state.category = "main";
    }
    const buckets = new Set(["attention", "active", "all"]);
    if (!buckets.has(state.bucket)) state.bucket = "all";
  }

  function renderDashboard() {
    renderCategories();
    renderBucketFilter();
    renderProjects();
    updateOperatorScene();
    const generatedAt = new Date(state.dashboard.generated_at);
    elements.updatedLabel.textContent = Number.isNaN(generatedAt.getTime())
      ? "Updated"
      : `Updated ${formatClockTime(generatedAt)}`;
  }

  function renderCategories() {
    elements.categoryTabs.replaceChildren();
    for (const category of state.dashboard.categories || []) {
      const button = createElement("button", "category-tab");
      button.type = "button";
      button.role = "tab";
      button.dataset.category = category.id;
      button.setAttribute("aria-selected", String(category.id === state.category));
      if (category.id === state.category) button.classList.add("selected");
      if (category.attention_count > 0) button.classList.add("has-attention");
      button.append(createElement("span", "tab-label", category.label));
      const countLabel = category.attention_count > 0
        ? `${category.attention_count}/${category.count}`
        : String(category.count);
      button.append(createElement("span", "tab-count", countLabel));
      button.addEventListener("click", () => {
        state.category = category.id;
        window.localStorage.setItem("lcr.mobile.category", state.category);
        renderDashboard();
      });
      elements.categoryTabs.append(button);
    }
  }

  function renderBucketFilter() {
    const counts = state.dashboard?.counts || {};
    elements.attentionCount.textContent = String(counts.attention || 0);
    elements.activeCount.textContent = String(counts.active || 0);
    elements.allCount.textContent = String(counts.all || 0);
    for (const button of elements.bucketFilter.querySelectorAll("button")) {
      const selected = button.dataset.bucket === state.bucket;
      button.classList.toggle("selected", selected);
      button.setAttribute("aria-pressed", String(selected));
    }
  }

  function visibleProjects() {
    const query = state.query.trim().toLocaleLowerCase();
    return (state.dashboard?.projects || []).filter((project) => {
      const inCategory = state.category === "all"
        || (state.category === "main" ? !project.category_id : project.category_id === state.category);
      const inBucket = state.bucket === "all" || project.bucket === state.bucket;
      const matchesQuery = !query
        || project.name.toLocaleLowerCase().includes(query)
        || project.summary.toLocaleLowerCase().includes(query)
        || project.path.toLocaleLowerCase().includes(query);
      return inCategory && inBucket && matchesQuery;
    });
  }

  function renderProjects() {
    const projects = visibleProjects();
    elements.projectList.replaceChildren();
    elements.dashboardState.hidden = true;

    const bucketLabels = {
      attention: "Needs attention",
      active: "Active work",
      all: "Projects",
    };
    elements.queueTitle.textContent = state.query ? "Search results" : bucketLabels[state.bucket];
    elements.queueCount.textContent = String(projects.length);

    if (projects.length === 0) {
      elements.dashboardState.hidden = false;
      elements.dashboardState.replaceChildren(createElement("p", "", state.query ? "No matching projects" : "No projects in this view"));
      return;
    }

    for (const project of projects) {
      const row = createElement("li", "project-row");
      const button = createElement("button", `project-button rack-row bucket-${project.bucket}`);
      button.type = "button";
      button.dataset.path = project.path;
      button.setAttribute("aria-label", `Open ${project.name}`);
      if (project.path === state.selectedPath) button.classList.add("selected");

      const lamp = createElement("span", `rack-lamp lamp ${projectLampClass(project)}`);
      lamp.setAttribute("aria-hidden", "true");
      button.append(lamp);

      const head = createElement("div", "project-row-head");
      head.append(createElement("span", "project-name", project.name));
      head.append(createElement("span", "project-time", project.last_activity_label));
      button.append(head);
      button.append(createElement("p", "project-summary", project.summary));

      const badges = createElement("div", "badge-row");
      for (const badge of (project.badges || []).slice(0, 4)) {
        badges.append(createBadge(badge));
      }
      button.append(badges);
      button.append(createElement("span", "project-chevron", ">"));
      button.addEventListener("click", () => openProject(project.path, true));
      row.append(button);
      elements.projectList.append(row);
    }
  }

  function projectLampClass(project) {
    const tones = new Set((project.badges || []).map((badge) => badge.tone));
    if (tones.has("danger") || tones.has("conflict")) return "red";
    if (project.bucket === "attention") return "amber";
    if (project.bucket === "active") return "cyan";
    return "green dim";
  }

  function createBadge(badge) {
    const node = createElement("span", "badge", badge.label);
    const semanticClass = toneClass(badge.tone);
    if (semanticClass) node.classList.add(semanticClass);
    return node;
  }

  async function openProject(path, updateHistory) {
    if (!path) return;
    state.selectedPath = path;
    elements.body.classList.add("detail-open");
    elements.detailView.hidden = false;
    elements.detailView.removeAttribute("aria-hidden");
    elements.detailContent.hidden = true;
    elements.detailState.replaceChildren(createElement("p", "", "Tuning project channel"));
    renderProjects();
    if (updateHistory) {
      window.history.pushState({ projectPath: path }, "", `#project=${encodeURIComponent(path)}`);
    }

    try {
      const detail = await fetchJSON(`/api/mobile/projects/detail?path=${encodeURIComponent(path)}`);
      if (state.selectedPath !== path) return;
      renderProjectDetail(detail);
    } catch (error) {
      if (state.selectedPath !== path) return;
      renderDetailError(error);
    }
  }

  function renderProjectDetail(detail) {
    const project = detail.project;
    elements.detailState.replaceChildren();
    elements.detailCategory.textContent = project.category_name || "Main";
    elements.detailTitle.textContent = project.name;
    elements.detailSummary.textContent = project.summary;
    elements.detailBadges.replaceChildren();
    for (const badge of project.badges || []) {
      elements.detailBadges.append(createBadge(badge));
    }
    renderDetailBlocks(detail.blocks || []);
    elements.detailContent.hidden = false;
    document.title = `${project.name} - Little Control Room`;
    window.scrollTo({ top: 0, behavior: "auto" });
  }

  function renderDetailBlocks(blocks) {
    elements.detailBlocks.replaceChildren();
    for (const block of blocks) {
      switch (block.kind) {
        case "field":
        case "wrapped_field":
          elements.detailBlocks.append(createDetailField(block.label, block.text, block.tone));
          break;
        case "field_group":
          for (const field of block.fields || []) {
            elements.detailBlocks.append(createDetailField(field.label, field.text, field.tone));
          }
          break;
        case "section":
          elements.detailBlocks.append(createElement("h3", "detail-section-title", block.text));
          break;
        case "bullet": {
          const bullet = createElement("p", "detail-bullet", block.text);
          const semanticClass = toneClass(block.tone);
          if (semanticClass) bullet.classList.add(semanticClass);
          elements.detailBlocks.append(bullet);
          break;
        }
        case "text": {
          const text = createElement("p", "detail-text", block.text);
          const semanticClass = toneClass(block.tone);
          if (semanticClass) text.classList.add(semanticClass);
          elements.detailBlocks.append(text);
          break;
        }
        default:
          break;
      }
    }
  }

  function createDetailField(label, value, tone) {
    const row = createElement("div", "detail-field");
    row.append(createElement("div", "detail-label", label));
    const valueNode = createElement("div", "detail-value", value);
    const semanticClass = toneClass(tone);
    if (semanticClass) valueNode.classList.add(semanticClass);
    row.append(valueNode);
    return row;
  }

  function closeProject(updateHistory) {
    state.selectedPath = "";
    elements.body.classList.remove("detail-open");
    elements.detailView.hidden = true;
    elements.detailView.setAttribute("aria-hidden", "true");
    elements.detailContent.hidden = true;
    elements.detailState.replaceChildren();
    document.title = "Little Control Room";
    renderProjects();
    if (updateHistory && window.location.hash.startsWith("#project=")) {
      window.history.pushState({}, "", `${window.location.pathname}${window.location.search}`);
    }
  }

  function renderDashboardError(error) {
    elements.dashboardState.hidden = false;
    elements.projectList.replaceChildren();
    const message = createElement("p", "", `Could not load projects: ${error.message}`);
    const retry = createElement("button", "error-action", "Try again");
    retry.type = "button";
    retry.addEventListener("click", () => loadDashboard(true));
    elements.dashboardState.replaceChildren(message, retry);
  }

  function renderDetailError(error) {
    const message = createElement("p", "", `Could not load project: ${error.message}`);
    const retry = createElement("button", "error-action", "Try again");
    retry.type = "button";
    retry.addEventListener("click", () => openProject(state.selectedPath, false));
    elements.detailState.replaceChildren(message, retry);
  }

  function setConnection(status, label) {
    state.connection = status;
    elements.connectionDot.className = "lamp connection-dot";
    if (status === "connecting") elements.connectionDot.classList.add("amber", "connecting");
    if (status === "offline") elements.connectionDot.classList.add("red", "offline");
    elements.connectionLabel.textContent = label;
    updateOperatorScene();
  }

  function updateOperatorScene() {
    const counts = state.dashboard?.counts || {};
    let scene = "idle";
    let count = String(counts.all || 0);
    let label = counts.all === 1 ? "Project monitored" : "Projects monitored";
    let copy = "No immediate calls on the switchboard.";
    let caption = "Station quiet";
    let lamp = "green";

    if (state.connection === "connecting") {
      scene = "connecting";
      count = "--";
      label = "Establishing link";
      copy = "Calling the local control room.";
      caption = "Link pending";
      lamp = "amber";
    } else if (state.connection === "offline") {
      scene = "offline";
      count = "--";
      label = "Link offline";
      copy = "The last switchboard reading may be stale.";
      caption = "Host unavailable";
      lamp = "red";
    } else if ((counts.attention || 0) > 0) {
      scene = "attention";
      count = String(counts.attention);
      label = counts.attention === 1 ? "Project needs attention" : "Projects need attention";
      copy = "The switchboard has flagged work for review.";
      caption = "Attention circuit";
      lamp = "amber";
    } else if ((counts.active || 0) > 0) {
      scene = "working";
      count = String(counts.active);
      label = counts.active === 1 ? "Project active" : "Projects active";
      copy = "Work is moving and the operator is standing by.";
      caption = "Live activity";
      lamp = "cyan";
    }

    elements.operatorBay.dataset.state = scene;
    elements.operatorCount.textContent = count;
    elements.operatorLabel.textContent = label;
    elements.operatorCopy.textContent = copy;
    elements.operatorCaption.textContent = caption;
    elements.operatorLamp.className = `lamp ${lamp}`;
    if (elements.operatorSprite.dataset.state !== scene) {
      elements.operatorSprite.dataset.state = scene;
      elements.operatorSprite.src = `/assets/operator-station.png?state=${encodeURIComponent(scene)}`;
    }
  }

  function connectEvents() {
    if (state.socket) state.socket.close();
    window.clearTimeout(state.reconnectTimer);
    const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
    const socket = new WebSocket(`${scheme}//${window.location.host}/events/ws`);
    state.socket = socket;
    setConnection("connecting", "Connecting");

    socket.addEventListener("open", () => setConnection("live", "Live"));
    socket.addEventListener("message", () => {
      window.clearTimeout(state.refreshTimer);
      state.refreshTimer = window.setTimeout(async () => {
        await loadDashboard(false);
        if (state.selectedPath) await openProject(state.selectedPath, false);
      }, 350);
    });
    socket.addEventListener("close", () => {
      if (state.socket !== socket) return;
      setConnection("offline", "Reconnecting");
      state.reconnectTimer = window.setTimeout(connectEvents, 2500);
    });
    socket.addEventListener("error", () => socket.close());
  }

  function openProjectFromLocation() {
    const match = window.location.hash.match(/^#project=(.+)$/);
    if (!match) {
      closeProject(false);
      return;
    }
    let path = "";
    try {
      path = decodeURIComponent(match[1]);
    } catch (_) {
      closeProject(false);
      return;
    }
    if (path !== state.selectedPath) openProject(path, false);
  }

  function formatClockTime(date) {
    return new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(date);
  }

  function updateSystemTime() {
    const now = new Date();
    elements.systemTime.dateTime = now.toISOString();
    elements.systemTime.textContent = formatClockTime(now);
  }

  elements.refreshButton.addEventListener("click", async () => {
    await loadDashboard(true);
    if (state.selectedPath) await openProject(state.selectedPath, false);
  });

  elements.bucketFilter.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-bucket]");
    if (!button) return;
    state.bucket = button.dataset.bucket;
    window.localStorage.setItem("lcr.mobile.bucket", state.bucket);
    renderDashboard();
  });

  elements.search.addEventListener("input", () => {
    state.query = elements.search.value;
    renderProjects();
  });

  elements.backButton.addEventListener("click", () => closeProject(true));
  window.addEventListener("popstate", openProjectFromLocation);
  window.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && state.selectedPath) closeProject(true);
  });

  updateSystemTime();
  window.setInterval(updateSystemTime, 30000);
  loadDashboard(true).then(openProjectFromLocation);
  connectEvents();
})();
