(() => {
  "use strict";

  const state = {
    dashboard: null,
    category: window.localStorage.getItem("lcr.mobile.category") || "main",
    query: "",
    selectedPath: "",
    selectedSessionID: "",
    selectedProjectDetail: null,
    projectDetailCache: new Map(),
    selectedProjectSessions: [],
    projectSessionsPath: "",
    projectSessionSignature: "",
    sessionDetailSignature: "",
    sessionTranscriptRevision: 0,
    transcriptMode: window.localStorage.getItem("lcr.mobile.transcript-mode") || "conversation",
    sessionEntries: [],
    sessionEmptyMessage: "",
    sessionLastEntryKey: "",
    sessionHasNewActivity: false,
    sessionStickToBottom: true,
    sessionInput: null,
    sessionSubmitting: false,
    sessionFeedback: "",
    sessionLive: false,
    sessionRequestID: 0,
    sessionStream: null,
    sessionStreamKey: "",
    sessionStreamConnected: false,
    quickPanel: "",
    quickPanelRequestID: 0,
    commandSuggestionRequestID: 0,
    socket: null,
    reconnectTimer: 0,
    refreshTimer: 0,
    connection: "connecting",
    authRequired: false,
    authenticated: false,
  };
  if (!["conversation", "all"].includes(state.transcriptMode)) state.transcriptMode = "conversation";

  const elements = {
    body: document.body,
    systemTime: document.getElementById("system-time"),
    commandButton: document.getElementById("command-button"),
    refreshButton: document.getElementById("refresh-button"),
    connectionDot: document.getElementById("connection-dot"),
    connectionLabel: document.getElementById("connection-label"),
    authStateLabel: document.getElementById("auth-state-label"),
    authView: document.getElementById("auth-view"),
    authLockState: document.getElementById("auth-lock-state"),
    authChecking: document.getElementById("auth-checking"),
    authCheckingLabel: document.getElementById("auth-checking-label"),
    authForm: document.getElementById("auth-form"),
    authCode: document.getElementById("auth-code"),
    authError: document.getElementById("auth-error"),
    authSubmit: document.getElementById("auth-submit"),
    authRetry: document.getElementById("auth-retry"),
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
    dashboardLiveChannels: document.getElementById("dashboard-live-channels"),
    dashboardLiveCount: document.getElementById("dashboard-live-count"),
    dashboardLiveList: document.getElementById("dashboard-live-list"),
    updatedLabel: document.getElementById("updated-label"),
    categoryTabs: document.getElementById("category-tabs"),
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
    projectSessionCount: document.getElementById("project-session-count"),
    projectSessionsState: document.getElementById("project-sessions-state"),
    projectSessionList: document.getElementById("project-session-list"),
    sessionView: document.getElementById("session-view"),
    sessionBackButton: document.getElementById("session-back-button"),
    sessionState: document.getElementById("session-state"),
    sessionActionDeck: document.getElementById("session-action-deck"),
    sessionProjectSummary: document.getElementById("session-project-summary"),
    sessionProjectSummaryLamp: document.getElementById("session-project-summary-lamp"),
    sessionProjectAssessment: document.getElementById("session-project-assessment"),
    sessionProjectSummaryText: document.getElementById("session-project-summary-text"),
    sessionStandby: document.getElementById("session-standby"),
    sessionStandbyList: document.getElementById("session-standby-list"),
    sessionContent: document.getElementById("session-content"),
    sessionProjectName: document.getElementById("session-project-name"),
    sessionLiveLamp: document.getElementById("session-live-lamp"),
    sessionProvider: document.getElementById("session-provider"),
    sessionTitle: document.getElementById("session-title"),
    sessionSummary: document.getElementById("session-summary"),
    sessionStatus: document.getElementById("session-status"),
    sessionID: document.getElementById("session-id"),
    sessionInstruments: document.getElementById("session-instruments"),
    sessionInstrumentToggle: document.getElementById("session-instrument-toggle"),
    sessionInstrumentSummary: document.getElementById("session-instrument-summary"),
    sessionInstrumentList: document.getElementById("session-instrument-list"),
    transcriptMode: document.getElementById("transcript-mode"),
    sessionFollowButton: document.getElementById("session-follow-button"),
    sessionUpdatedLabel: document.getElementById("session-updated-label"),
    sessionTruncated: document.getElementById("session-truncated"),
    sessionTranscript: document.getElementById("session-transcript"),
    sessionComposer: document.getElementById("session-composer"),
    sessionComposerLamp: document.getElementById("session-composer-lamp"),
    sessionComposerState: document.getElementById("session-composer-state"),
    sessionComposerMode: document.getElementById("session-composer-mode"),
    sessionMessage: document.getElementById("session-message"),
    sessionSendButton: document.getElementById("session-send-button"),
    sessionComposerFeedback: document.getElementById("session-composer-feedback"),
    sessionReadonlyStrip: document.getElementById("session-readonly-strip"),
    sessionReadonlyLabel: document.getElementById("session-readonly-label"),
    sessionLinkLabel: document.getElementById("session-link-label"),
    quickPanelDialog: document.getElementById("quick-panel-dialog"),
    quickPanelKicker: document.getElementById("quick-panel-kicker"),
    quickPanelTitle: document.getElementById("quick-panel-title"),
    quickPanelClose: document.getElementById("quick-panel-close"),
    quickPanelStatus: document.getElementById("quick-panel-status"),
    quickPanelContent: document.getElementById("quick-panel-content"),
    protectedViews: document.querySelectorAll(".dashboard-view, .detail-view, .detail-placeholder, .session-view"),
  };

  class AuthRequiredError extends Error {
    constructor() {
      super("Mobile pairing required");
      this.name = "AuthRequiredError";
    }
  }

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
    if (response.status === 401) {
      showAuthGate();
      throw new AuthRequiredError();
    }
    if (!response.ok) {
      const message = (await response.text()).trim();
      throw new Error(message || `Request failed (${response.status})`);
    }
    return response.json();
  }

  async function postJSON(url, body) {
    const response = await window.fetch(url, {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      cache: "no-store",
      body: JSON.stringify(body),
    });
    if (response.status === 401) {
      showAuthGate();
      throw new AuthRequiredError();
    }
    if (!response.ok) {
      const message = (await response.text()).trim();
      throw new Error(message || `Request failed (${response.status})`);
    }
    return response.json();
  }

  function isAuthRequiredError(error) {
    return error instanceof AuthRequiredError;
  }

  function showAuthGate(message = "") {
    state.authRequired = true;
    state.authenticated = false;
    window.clearTimeout(state.reconnectTimer);
    closeSessionStream();
    closeQuickPanel();
    if (state.socket) {
      const socket = state.socket;
      state.socket = null;
      socket.close();
    }
    elements.body.classList.remove("auth-pending");
    elements.body.classList.add("auth-locked");
    for (const view of elements.protectedViews) view.inert = true;
    elements.authView.hidden = false;
    elements.authChecking.hidden = true;
    elements.authForm.hidden = false;
    elements.authRetry.hidden = true;
    elements.authLockState.textContent = "Locked";
    elements.authLockState.className = "metal-status tone-warning";
    elements.authStateLabel.textContent = "Locked";
    elements.authError.textContent = message;
    setConnection("offline", "Locked");
  }

  function showAuthLinkFailure(message) {
    state.authenticated = false;
    elements.body.classList.remove("auth-pending");
    elements.body.classList.add("auth-locked");
    for (const view of elements.protectedViews) view.inert = true;
    elements.authView.hidden = false;
    elements.authChecking.hidden = false;
    elements.authForm.hidden = true;
    elements.authRetry.hidden = false;
    elements.authCheckingLabel.textContent = message;
    elements.authLockState.textContent = "Offline";
    elements.authLockState.className = "metal-status tone-danger";
    elements.authStateLabel.textContent = "Offline";
    setConnection("offline", "Offline");
  }

  function releaseAuthGate(status) {
    state.authRequired = Boolean(status.required);
    state.authenticated = true;
    elements.body.classList.remove("auth-pending", "auth-locked");
    for (const view of elements.protectedViews) view.inert = false;
    elements.authView.hidden = true;
    elements.authForm.hidden = true;
    elements.authChecking.hidden = true;
    elements.authRetry.hidden = true;
    elements.authError.textContent = "";
    elements.authCode.value = "";
    elements.authLockState.textContent = "Paired";
    elements.authLockState.className = "metal-status tone-positive";
    elements.authStateLabel.textContent = status.required ? "Paired" : "Local";
  }

  async function readAuthStatus() {
    const response = await window.fetch("/api/mobile/auth/status", {
      headers: { Accept: "application/json" },
      cache: "no-store",
    });
    if (!response.ok) throw new Error(`Link check failed (${response.status})`);
    return response.json();
  }

  async function bootstrap() {
    elements.body.classList.add("auth-pending");
    elements.authView.hidden = false;
    elements.authChecking.hidden = false;
    elements.authForm.hidden = true;
    elements.authRetry.hidden = true;
    elements.authCheckingLabel.textContent = "Checking control-room link";
    elements.authStateLabel.textContent = "Checking";
    try {
      const status = await readAuthStatus();
      if (status.required && !status.authenticated) {
        showAuthGate();
        return;
      }
      releaseAuthGate(status);
      await startAuthenticatedApp();
    } catch (error) {
      showAuthLinkFailure(error.message || "Control room unavailable");
    }
  }

  async function startAuthenticatedApp() {
    if (!state.authenticated) return;
    connectEvents();
    await loadDashboard(true);
    if (state.authenticated) {
      closeProject(false);
      await openRouteFromLocation();
    }
  }

  async function loadDashboard(showSpinner = true) {
    if (showSpinner) elements.refreshButton.classList.add("spinning");
    try {
      state.dashboard = await fetchJSON("/api/mobile/dashboard");
      ensureSelectedCategory();
      renderDashboard();
    } catch (error) {
      if (isAuthRequiredError(error)) return;
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
  }

  function renderDashboard() {
    renderCategories();
    renderDashboardLiveSessions();
    renderProjects();
    updateOperatorScene();
    const generatedAt = new Date(state.dashboard.generated_at);
    elements.updatedLabel.textContent = Number.isNaN(generatedAt.getTime())
      ? "Updated"
      : `Updated ${formatClockTime(generatedAt)}`;
  }

  function renderDashboardLiveSessions() {
    const sessions = state.dashboard?.live_sessions || [];
    elements.dashboardLiveList.replaceChildren();
    elements.dashboardLiveChannels.hidden = sessions.length === 0;
    elements.dashboardLiveCount.textContent = sessions.length === 1 ? "1 live" : `${sessions.length} live`;
    if (sessions.length === 0) return;

    for (const session of sessions) {
      const button = createElement("button", "project-session-button dashboard-session-button");
      button.type = "button";
      const projectName = session.project_name || projectNameForPath(session.project_path);
      button.setAttribute("aria-label", `Open live ${session.provider_label} session for ${projectName}`);

      const lamp = createElement("span", `lamp session-rack-lamp ${sessionLampClass(session)}`);
      lamp.setAttribute("aria-hidden", "true");
      button.append(lamp);

      const content = createElement("span", "project-session-content");
      const title = createElement("span", "project-session-title");
      title.append(createElement("strong", "", projectName));
      title.append(createElement("span", "live-flag", session.provider_label));
      content.append(title);
      content.append(createElement("span", "project-session-summary", session.summary));
      button.append(content);

      const meta = createElement("span", "project-session-meta");
      const status = createElement("span", "metal-status", session.status.label);
      const semanticClass = toneClass(session.status.tone);
      if (semanticClass) status.classList.add(semanticClass);
      meta.append(status);
      meta.append(createElement("span", "project-session-time", session.last_activity_label));
      button.append(meta);
      button.append(createElement("span", "project-session-chevron", ">"));
      button.addEventListener("click", () => void openDashboardSession(session));
      elements.dashboardLiveList.append(button);
    }
  }

  async function openDashboardSession(session) {
    if (!session?.project_path || !session.id) return;
    const detail = await prepareProjectContext(session.project_path);
    if (!detail || state.selectedPath !== session.project_path) return;
    state.selectedProjectSessions = [session];
    await openSession(session.id, false);
    window.history.pushState(
      { projectPath: session.project_path, sessionID: session.id },
      "",
      `#session=${encodeURIComponent(session.id)}&project=${encodeURIComponent(session.project_path)}`,
    );
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
      const countLabel = String(category.count);
      button.append(createElement("span", "tab-count", countLabel));
      button.addEventListener("click", () => {
        state.category = category.id;
        window.localStorage.setItem("lcr.mobile.category", state.category);
        renderDashboard();
      });
      elements.categoryTabs.append(button);
    }
  }

  function visibleProjects() {
    const query = state.query.trim().toLocaleLowerCase();
    const tabProjects = (state.dashboard?.projects || []).filter((project) => project.tab_id === state.category);
    if (!query) return tabProjects;
    const matchingRoots = new Set();
    for (const project of tabProjects) {
      const matchesQuery = project.name.toLocaleLowerCase().includes(query)
        || (project.list_name || "").toLocaleLowerCase().includes(query)
        || project.summary.toLocaleLowerCase().includes(query)
        || project.path.toLocaleLowerCase().includes(query);
      if (matchesQuery) matchingRoots.add(project.worktree_root_path || project.path);
    }
    return tabProjects.filter((project) => matchingRoots.has(project.worktree_root_path || project.path));
  }

  function renderProjects() {
    const projects = visibleProjects();
    const liveSessions = new Map((state.dashboard?.live_sessions || []).map((session) => [session.project_path, session]));
    elements.projectList.replaceChildren();
    elements.dashboardState.hidden = true;

    const selectedTab = (state.dashboard?.categories || []).find((category) => category.id === state.category);
    elements.queueTitle.textContent = state.query ? "Search results" : `${selectedTab?.label || "Main"} projects`;
    elements.queueCount.textContent = String(projects.length);

    if (projects.length === 0) {
      elements.dashboardState.hidden = false;
      elements.dashboardState.replaceChildren(createElement("p", "", state.query ? "No matching projects" : "No projects in this view"));
      return;
    }

    for (const project of projects) {
      const hierarchyClass = project.worktree_role ? ` worktree-${project.worktree_role}` : "";
      const row = createElement("li", `project-row${hierarchyClass}`);
      const button = createElement("button", `project-button rack-row bucket-${project.bucket}${hierarchyClass}`);
      button.type = "button";
      button.dataset.path = project.path;
      button.setAttribute("aria-label", `Open ${project.name}`);
      if (project.path === state.selectedPath) button.classList.add("selected");

      const lamp = createElement("span", `rack-lamp lamp ${projectLampClass(project)}`);
      lamp.setAttribute("aria-hidden", "true");
      button.append(lamp);

      const core = createElement("span", "project-core");
      const head = createElement("div", "project-row-head");
      const nameLine = createElement("span", "project-name-line");
      if (project.worktree_role === "child") nameLine.append(createElement("span", "worktree-branch", "↳"));
      nameLine.append(createElement("span", "project-name", project.list_name || project.name));
      if (project.worktree_role === "root" && project.linked_count > 0) {
        nameLine.append(createElement("span", "worktree-count", `${project.linked_count} WT`));
      }
      head.append(nameLine);
      head.append(createElement("span", "project-time", project.last_activity_label));
      core.append(head);
      core.append(createElement("span", "project-summary", project.summary));

      const context = createElement("span", "project-context");
      if (project.open_todo_count > 0) {
        context.append(createElement("span", "project-mini-label", `${project.open_todo_count} TODO`));
      }
      if (context.childElementCount > 0) core.append(context);
      button.append(core);

      button.append(createAssessmentSignal(project.assessment));
      button.append(createAgentSignal(project, liveSessions.get(project.path)));
      button.append(createFlagSignal(project));
      button.append(createElement("span", "project-chevron", ">"));
      button.addEventListener("click", () => openProject(project.path, true));
      row.append(button);
      elements.projectList.append(row);
    }
  }

  function createAssessmentSignal(assessment = {}) {
    let glyph = "·";
    switch (assessment.tone) {
      case "positive":
        glyph = "✓";
        break;
      case "warning":
        glyph = "?";
        break;
      case "danger":
      case "conflict":
        glyph = "!";
        break;
      case "info":
        glyph = "↻";
        break;
      default:
        break;
    }
    return createProjectSignal("assessment", glyph, assessment.label || "No assessment", assessment.tone);
  }

  function createAgentSignal(project, liveSession) {
    const provider = liveSession?.provider_label || project.source_label || "";
    const active = Boolean(liveSession?.live);
    const status = liveSession?.status?.label || (provider ? "No live engineer" : "No engineer");
    const signal = createProjectSignal("agent", active ? "◆" : provider ? "◇" : "·", providerTag(provider), active ? "positive" : "muted");
    signal.classList.toggle("signal-live", active);
    signal.title = `${provider || "Engineer"}: ${status}`;
    signal.setAttribute("aria-label", signal.title);
    return signal;
  }

  function createFlagSignal(project) {
    const repositoryFlags = (project.badges || []).filter((badge) => badge.kind === "repository");
    if (repositoryFlags.length > 0) {
      const flag = repositoryFlags[0];
      return createProjectSignal("flags", "⚑", flag.label, flag.tone);
    }
    if (project.open_todo_count > 0) {
      return createProjectSignal("flags", "□", String(project.open_todo_count), "muted", `${project.open_todo_count} open TODO`);
    }
    return createProjectSignal("flags", "·", "clear", "muted", "No project flags");
  }

  function createProjectSignal(kind, glyph, value, tone, accessibleLabel = "") {
    const signal = createElement("span", `project-signal signal-${kind}`);
    const semanticClass = toneClass(tone);
    if (semanticClass) signal.classList.add(semanticClass);
    signal.append(createElement("span", "project-signal-glyph", glyph));
    signal.append(createElement("span", "project-signal-value", compactSignalValue(value)));
    signal.title = accessibleLabel || String(value || "");
    signal.setAttribute("aria-label", signal.title);
    return signal;
  }

  function compactSignalValue(value) {
    const label = String(value || "").trim();
    if (!label) return "—";
    const normalized = label.toLocaleLowerCase();
    const aliases = new Map([
      ["no assessment", "none"],
      ["not assessed", "none"],
      ["follow up", "next"],
      ["follow-up", "next"],
      ["in progress", "work"],
      ["assessing", "work"],
      ["working", "work"],
      ["claude code", "cc"],
      ["opencode", "oc"],
      ["lcagent", "lc"],
      ["codex", "cx"],
    ]);
    return aliases.get(normalized) || normalized;
  }

  function providerTag(provider) {
    return compactSignalValue(provider) === "none" ? "—" : compactSignalValue(provider);
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

  async function loadProjectSessions(path, showLoading = false) {
    if (!path) return;
    const reset = showLoading || state.projectSessionsPath !== path;
    if (reset) {
      state.projectSessionsPath = path;
      state.projectSessionSignature = "";
      elements.projectSessionCount.textContent = "Scanning";
      elements.projectSessionsState.hidden = false;
      elements.projectSessionsState.textContent = "Reading session rack";
      elements.projectSessionList.replaceChildren();
    }
    try {
      const surface = await fetchJSON(`/api/mobile/projects/sessions?path=${encodeURIComponent(path)}`);
      if (state.selectedPath !== path) return;
      const sessions = surface.sessions || [];
      state.selectedProjectSessions = sessions;
      const signature = JSON.stringify(sessions.map((session) => [
        session.id,
        session.live,
        session.status?.label,
        session.summary,
        session.last_activity_at,
        session.transcript_revision,
      ]));
      if (!reset && signature === state.projectSessionSignature) return;
      state.projectSessionSignature = signature;
      renderProjectSessions(sessions);
    } catch (error) {
      if (isAuthRequiredError(error)) return;
      if (state.selectedPath !== path) return;
      elements.projectSessionCount.textContent = "Unavailable";
      elements.projectSessionsState.hidden = false;
      elements.projectSessionsState.textContent = `Could not read sessions: ${error.message}`;
      elements.projectSessionList.replaceChildren();
    }
  }

  function renderProjectSessions(sessions) {
    elements.projectSessionList.replaceChildren();
    elements.projectSessionCount.textContent = sessions.length === 1 ? "1 channel" : `${sessions.length} channels`;
    if (sessions.length === 0) {
      elements.projectSessionsState.hidden = false;
      elements.projectSessionsState.textContent = "No engineer sessions recorded";
      return;
    }
    elements.projectSessionsState.hidden = true;

    for (const session of sessions.slice(0, 4)) {
      const button = createElement("button", "project-session-button");
      button.type = "button";
      button.setAttribute("aria-label", `Open ${session.provider_label} session ${session.display_id}`);

      const lamp = createElement("span", `lamp session-rack-lamp ${sessionLampClass(session)}`);
      lamp.setAttribute("aria-hidden", "true");
      button.append(lamp);

      const content = createElement("span", "project-session-content");
      const title = createElement("span", "project-session-title");
      title.append(createElement("strong", "", session.provider_label));
      title.append(createElement("span", "session-short-id", session.display_id));
      if (session.live) title.append(createElement("span", "live-flag", "Live"));
      content.append(title);
      content.append(createElement("span", "project-session-summary", session.summary));
      button.append(content);

      const meta = createElement("span", "project-session-meta");
      const status = createElement("span", "metal-status", session.status.label);
      const semanticClass = toneClass(session.status.tone);
      if (semanticClass) status.classList.add(semanticClass);
      meta.append(status);
      meta.append(createElement("span", "project-session-time", session.last_activity_label));
      button.append(meta);
      button.append(createElement("span", "project-session-chevron", ">"));
      button.addEventListener("click", () => openSession(session.id, true));
      elements.projectSessionList.append(button);
    }
  }

  function sessionLampClass(session) {
    if (session.status.tone === "danger") return "red";
    if (session.status.tone === "warning") return "amber";
    if (session.live) return "cyan";
    if (session.status.tone === "positive") return "green";
    return "green dim";
  }

  function sessionFirstLayout() {
    return window.matchMedia("(max-width: 899px)").matches;
  }

  async function prepareProjectContext(path) {
    if (!path) return null;
    state.selectedPath = path;
    renderProjects();
    let detail = state.projectDetailCache.get(path) || null;
    try {
      detail = await fetchJSON(`/api/mobile/projects/detail?path=${encodeURIComponent(path)}`);
      if (state.selectedPath !== path) return null;
      state.projectDetailCache.set(path, detail);
      state.selectedProjectDetail = detail;
      renderSessionProjectContext(detail);
      return detail;
    } catch (error) {
      if (isAuthRequiredError(error)) return null;
      if (state.selectedPath === path) renderSessionProjectContext(detail);
      throw error;
    }
  }

  async function openProjectSessionFirst(path, updateHistory) {
    if (!path) return;
    if (state.selectedSessionID) hideSession();
    state.selectedPath = path;
    state.selectedProjectSessions = [];
    elements.body.classList.add("detail-open", "session-open");
    elements.sessionView.hidden = false;
    elements.sessionView.removeAttribute("aria-hidden");
    elements.sessionContent.hidden = true;
    elements.sessionStandby.hidden = true;
    elements.sessionActionDeck.hidden = false;
    elements.sessionProjectSummary.hidden = true;
    elements.sessionProjectName.textContent = projectNameForPath(path);
    elements.sessionState.replaceChildren(createElement("p", "", "Tuning engineer channel"));
    renderProjects();

    try {
      const [detail, sessionsSurface] = await Promise.all([
        prepareProjectContext(path),
        fetchJSON(`/api/mobile/projects/sessions?path=${encodeURIComponent(path)}`),
      ]);
      if (!detail || state.selectedPath !== path) return;
      const sessions = sessionsSurface.sessions || [];
      state.selectedProjectSessions = sessions;
      state.projectSessionsPath = path;
      const liveSession = sessions.find((session) => session.live);
      if (liveSession) {
        await openSession(liveSession.id, false);
        if (updateHistory && state.selectedPath === path && state.selectedSessionID === liveSession.id) {
          window.history.pushState(
            { projectPath: path, sessionID: liveSession.id },
            "",
            `#session=${encodeURIComponent(liveSession.id)}&project=${encodeURIComponent(path)}`,
          );
        }
        return;
      }
      showSessionStandby(sessions);
      if (updateHistory) {
        window.history.pushState({ projectPath: path }, "", `#project=${encodeURIComponent(path)}`);
      }
    } catch (error) {
      if (isAuthRequiredError(error) || state.selectedPath !== path) return;
      elements.sessionState.replaceChildren();
      showSessionStandby([], `Could not read engineer sessions: ${error.message}`);
      if (updateHistory) {
        window.history.pushState({ projectPath: path }, "", `#project=${encodeURIComponent(path)}`);
      }
    }
  }

  function showSessionStandby(sessions, message = "") {
    closeSessionStream();
    state.selectedSessionID = "";
    elements.sessionState.replaceChildren();
    elements.sessionContent.hidden = true;
    elements.sessionActionDeck.hidden = false;
    elements.sessionStandby.hidden = false;
    const copy = elements.sessionStandby.querySelector("p:last-child");
    copy.textContent = message || "Open a recorded channel below, or use the slash control when a desktop-hosted session is available.";
    elements.sessionStandbyList.replaceChildren();
    const recorded = (sessions || []).filter((session) => !session.live).slice(0, 4);
    if (recorded.length === 0) {
      elements.sessionStandbyList.append(createElement("p", "panel-empty", "No recorded engineer channels were found."));
      return;
    }
    for (const session of recorded) {
      const button = createElement("button", "project-session-button");
      button.type = "button";
      button.append(createElement("span", `lamp session-rack-lamp ${sessionLampClass(session)}`));
      const content = createElement("span", "project-session-content");
      const title = createElement("span", "project-session-title");
      title.append(createElement("strong", "", session.provider_label));
      title.append(createElement("span", "session-short-id", session.display_id));
      content.append(title, createElement("span", "project-session-summary", session.summary));
      button.append(content, createElement("span", "project-session-chevron", ">"));
      button.addEventListener("click", () => void openSession(session.id, true));
      elements.sessionStandbyList.append(button);
    }
  }

  async function openProject(path, updateHistory, forceDetail = false) {
    if (!path) return;
    if (sessionFirstLayout() && !forceDetail) {
      await openProjectSessionFirst(path, updateHistory);
      return;
    }
    if (state.selectedSessionID && (updateHistory || path !== state.selectedPath)) hideSession();
    state.selectedPath = path;
    elements.body.classList.add("detail-open");
    elements.detailView.hidden = false;
    elements.detailView.removeAttribute("aria-hidden");
    elements.detailContent.hidden = true;
    elements.detailState.replaceChildren(createElement("p", "", "Tuning project channel"));
    renderProjects();
    void loadProjectSessions(path, state.projectSessionsPath !== path);
    if (updateHistory) {
      window.history.pushState({ projectPath: path }, "", `#project=${encodeURIComponent(path)}`);
    }

    try {
      const detail = await fetchJSON(`/api/mobile/projects/detail?path=${encodeURIComponent(path)}`);
      if (state.selectedPath !== path) return;
      state.projectDetailCache.set(path, detail);
      state.selectedProjectDetail = detail;
      renderProjectDetail(detail);
    } catch (error) {
      if (isAuthRequiredError(error)) return;
      if (state.selectedPath !== path) return;
      renderDetailError(error);
    }
  }

  function renderProjectDetail(detail) {
    const project = detail.project;
    state.projectDetailCache.set(project.path, detail);
    if (state.selectedPath === project.path) state.selectedProjectDetail = detail;
    renderSessionProjectContext(detail);
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

  function renderSessionProjectContext(detail) {
    const project = detail?.project;
    if (!project) {
      elements.sessionProjectSummary.hidden = true;
      return;
    }
    state.selectedProjectDetail = detail;
    elements.sessionProjectName.textContent = project.name || projectNameForPath(project.path);
    elements.sessionProjectSummary.hidden = false;
    elements.sessionProjectSummaryText.textContent = project.summary || "No recent assessment summary.";
    elements.sessionProjectAssessment.textContent = project.assessment?.label || "Not assessed";
    elements.sessionProjectAssessment.className = "metal-status";
    const assessmentClass = toneClass(project.assessment?.tone);
    if (assessmentClass) elements.sessionProjectAssessment.classList.add(assessmentClass);
    elements.sessionProjectSummaryLamp.className = `lamp ${projectSummaryLampClass(project)}`;
  }

  function projectSummaryLampClass(project) {
    switch (project?.assessment?.tone) {
      case "danger":
      case "conflict":
        return "red";
      case "warning":
        return "amber";
      case "info":
        return "cyan";
      default:
        return "green";
    }
  }

  function renderDetailBlocks(blocks) {
    elements.detailBlocks.replaceChildren();
    appendDetailBlocks(elements.detailBlocks, blocks);
  }

  function appendDetailBlocks(container, blocks) {
    for (const block of blocks) {
      switch (block.kind) {
        case "field":
        case "wrapped_field":
          container.append(createDetailField(block.label, block.text, block.tone));
          break;
        case "field_group":
          for (const field of block.fields || []) {
            container.append(createDetailField(field.label, field.text, field.tone));
          }
          break;
        case "section":
          container.append(createElement("h3", "detail-section-title", block.text));
          break;
        case "bullet": {
          const bullet = createElement("p", "detail-bullet", block.text);
          const semanticClass = toneClass(block.tone);
          if (semanticClass) bullet.classList.add(semanticClass);
          container.append(bullet);
          break;
        }
        case "text": {
          const text = createElement("p", "detail-text", block.text);
          const semanticClass = toneClass(block.tone);
          if (semanticClass) text.classList.add(semanticClass);
          container.append(text);
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

  function quickPanelDefinition(kind) {
    const definitions = {
      command: ["Slash command circuit", "Commands"],
      session: ["Live engineer telemetry", "Session information"],
      details: ["Shared project surface", "Project details"],
      todo: ["Repository work register", "TODOs"],
      runtime: ["Process control bank", "Runtime"],
    };
    return definitions[kind] || ["Project instrument", "Quick panel"];
  }

  function openQuickPanel(kind) {
    if (!kind) return;
    const [kicker, title] = quickPanelDefinition(kind);
    state.quickPanel = kind;
    state.quickPanelRequestID++;
    elements.quickPanelKicker.textContent = kicker;
    elements.quickPanelTitle.textContent = title;
    elements.quickPanelStatus.textContent = "";
    elements.quickPanelStatus.className = "quick-panel-status";
    elements.quickPanelContent.replaceChildren();
    for (const button of elements.sessionActionDeck.querySelectorAll("[data-quick-panel]")) {
      button.classList.toggle("active", button.dataset.quickPanel === kind);
    }
    if (!elements.quickPanelDialog.open) elements.quickPanelDialog.showModal();
    switch (kind) {
      case "command":
        renderCommandPanel();
        break;
      case "session":
        void loadSessionSidebarPanel();
        break;
      case "details":
        void loadDetailsPanel();
        break;
      case "todo":
        void loadTodoPanel();
        break;
      case "runtime":
        void loadRuntimePanel();
        break;
      default:
        setQuickPanelStatus("Unknown panel", "danger");
        break;
    }
  }

  function closeQuickPanel() {
    if (!elements.quickPanelDialog?.open) return;
    state.quickPanel = "";
    state.quickPanelRequestID++;
    state.commandSuggestionRequestID++;
    elements.quickPanelDialog.close();
    for (const button of elements.sessionActionDeck.querySelectorAll("[data-quick-panel]")) {
      button.classList.remove("active");
    }
  }

  function setQuickPanelStatus(message, tone = "") {
    elements.quickPanelStatus.textContent = message || "";
    elements.quickPanelStatus.className = "quick-panel-status";
    const semanticClass = toneClass(tone);
    if (semanticClass) elements.quickPanelStatus.classList.add(semanticClass);
  }

  function renderPanelLoading(message = "Reading instrument") {
    const stateNode = createElement("div", "dashboard-state");
    const lines = createElement("div", "loading-lines");
    lines.setAttribute("aria-hidden", "true");
    lines.append(document.createElement("span"), document.createElement("span"), document.createElement("span"));
    stateNode.append(lines, createElement("p", "", message));
    elements.quickPanelContent.replaceChildren(stateNode);
  }

  function renderPanelError(error, retry) {
    const message = createElement("p", "panel-empty", error.message || String(error));
    elements.quickPanelContent.replaceChildren(message);
    setQuickPanelStatus("Instrument unavailable", "warning");
    if (retry) {
      const button = createElement("button", "error-action", "Try again");
      button.type = "button";
      button.addEventListener("click", retry);
      elements.quickPanelContent.append(button);
    }
  }

  function createPanelSection(section, index = 0) {
    const details = createElement("details", "panel-section");
    details.open = index === 0 || section.id === "summary";
    const summary = document.createElement("summary");
    summary.append(createElement("span", "", section.title || "Readout"));
    if (section.summary) summary.append(createElement("span", "panel-section-summary", section.summary));
    const body = createElement("div", "panel-section-body");
    appendDetailBlocks(body, section.blocks || []);
    details.append(summary, body);
    return details;
  }

  function renderPanelSections(sections) {
    const fragment = document.createDocumentFragment();
    (sections || []).forEach((section, index) => fragment.append(createPanelSection(section, index)));
    if (!fragment.childNodes.length) fragment.append(createElement("p", "panel-empty", "No instrument data is available."));
    elements.quickPanelContent.replaceChildren(fragment);
  }

  async function loadSessionSidebarPanel() {
    if (!state.selectedPath) {
      renderPanelError(new Error("Select a project first."));
      return;
    }
    const requestID = ++state.quickPanelRequestID;
    renderPanelLoading("Reading live session telemetry");
    try {
      const surface = await fetchJSON(`/api/mobile/projects/sidebar?path=${encodeURIComponent(state.selectedPath)}`);
      if (requestID !== state.quickPanelRequestID || state.quickPanel !== "session") return;
      elements.quickPanelTitle.textContent = `${surface.session.provider_label} session`;
      setQuickPanelStatus(`Updated ${formatClockTime(new Date(surface.updated_at))}`);
      renderPanelSections(surface.sections || []);
    } catch (error) {
      if (isAuthRequiredError(error) || requestID !== state.quickPanelRequestID) return;
      renderPanelError(error, () => void loadSessionSidebarPanel());
    }
  }

  async function loadDetailsPanel() {
    if (!state.selectedPath) {
      renderPanelError(new Error("Select a project first."));
      return;
    }
    const requestID = ++state.quickPanelRequestID;
    renderPanelLoading("Reading shared project details");
    try {
      const detail = await fetchJSON(`/api/mobile/projects/detail?path=${encodeURIComponent(state.selectedPath)}`);
      if (requestID !== state.quickPanelRequestID || state.quickPanel !== "details") return;
      state.projectDetailCache.set(state.selectedPath, detail);
      state.selectedProjectDetail = detail;
      renderSessionProjectContext(detail);
      renderDetailsPanel(detail);
    } catch (error) {
      if (isAuthRequiredError(error) || requestID !== state.quickPanelRequestID) return;
      renderPanelError(error, () => void loadDetailsPanel());
    }
  }

  function renderDetailsPanel(detail) {
    const project = detail.project || {};
    elements.quickPanelTitle.textContent = project.name || "Project details";
    setQuickPanelStatus("Shared with the TUI project-detail surface");
    const plate = createElement("header", "detail-header project-plate");
    const top = createElement("div", "project-plate-top");
    const copy = document.createElement("div");
    copy.append(createElement("p", "instrument-label", project.category_name || "Main"));
    copy.append(createElement("h2", "", project.name || "Project"));
    top.append(copy, createElement("span", "plate-index", "P"));
    plate.append(top, createElement("p", "detail-summary", project.summary || "No recent assessment summary."));
    const badges = createElement("div", "badge-row status-lamps");
    for (const badge of project.badges || []) badges.append(createBadge(badge));
    plate.append(badges);
    const section = {
      id: "details",
      title: "Project instruments",
      summary: project.assessment?.label || "Details",
      blocks: detail.blocks || [],
    };
    elements.quickPanelContent.replaceChildren(plate, createPanelSection(section, 0));
  }

  async function loadTodoPanel() {
    if (!state.selectedPath) {
      renderPanelError(new Error("Select a project first."));
      return;
    }
    const requestID = ++state.quickPanelRequestID;
    renderPanelLoading("Reading repository TODOs");
    try {
      const surface = await fetchJSON(`/api/mobile/projects/todos?path=${encodeURIComponent(state.selectedPath)}`);
      if (requestID !== state.quickPanelRequestID || state.quickPanel !== "todo") return;
      renderTodoPanel(surface);
    } catch (error) {
      if (isAuthRequiredError(error) || requestID !== state.quickPanelRequestID) return;
      renderPanelError(error, () => void loadTodoPanel());
    }
  }

  function renderTodoPanel(surface) {
    elements.quickPanelTitle.textContent = surface.scope_label || "TODOs";
    setQuickPanelStatus(surface.write_enabled
      ? "Phone control armed · changes sync with the TUI"
      : "Read only · enable Phone control in Mobile settings");
    const root = createElement("div", "todo-panel");
    const toolbar = createElement("div", "todo-toolbar");
    toolbar.append(createElement("span", "", `${surface.open_count} open · ${surface.done_count} resolved`));
    if (surface.done_count > 0 && surface.write_enabled) {
      const purge = createElement("button", "panel-action-button danger", "Clear resolved");
      purge.type = "button";
      purge.addEventListener("click", () => {
        if (window.confirm("Delete all resolved TODOs?")) void mutateTodo({ action: "purge_done" });
      });
      toolbar.append(purge);
    } else {
      toolbar.append(createElement("span", "", surface.scope_project?.name || "Repository"));
    }
    root.append(toolbar);

    if (surface.write_enabled) root.append(createTodoEditor());
    const list = createElement("div", "todo-list");
    if (!(surface.todos || []).length) {
      list.append(createElement("p", "panel-empty", "No TODOs yet."));
    } else {
      for (const todo of surface.todos) list.append(createTodoItem(todo, surface.write_enabled));
    }
    root.append(list);
    elements.quickPanelContent.replaceChildren(root);
  }

  function createTodoEditor(todo = null) {
    const well = createElement("form", "todo-editor-well");
    well.dataset.todoId = todo?.id ? String(todo.id) : "";
    const row = createElement("div", "todo-editor-row");
    const input = document.createElement("input");
    input.className = "todo-editor-input";
    input.type = "text";
    input.maxLength = 12000;
    input.placeholder = todo ? "Edit TODO" : "Add a TODO";
    input.setAttribute("aria-label", todo ? "Edit TODO" : "New TODO");
    input.value = todo?.text || "";
    const save = createElement("button", "todo-save-button", todo ? "Save" : "Add");
    save.type = "submit";
    save.disabled = input.value.trim() === "";
    input.addEventListener("input", () => { save.disabled = input.value.trim() === ""; });
    well.addEventListener("submit", (event) => {
      event.preventDefault();
      const text = input.value.trim();
      if (!text) return;
      void mutateTodo({ action: todo ? "update" : "add", todo_id: todo?.id, text });
    });
    row.append(input, save);
    well.append(row);
    if (todo) {
      const cancel = createElement("button", "panel-action-button", "Cancel edit");
      cancel.type = "button";
      cancel.addEventListener("click", () => void loadTodoPanel());
      well.append(cancel);
      window.requestAnimationFrame(() => input.focus());
    }
    return well;
  }

  function createTodoItem(todo, writeEnabled) {
    const item = createElement("article", `todo-item${todo.done ? " done" : ""}`);
    const main = createElement("div", "todo-item-main");
    const toggle = createElement("button", "todo-toggle", todo.done ? "✓" : "□");
    toggle.type = "button";
    toggle.disabled = !writeEnabled;
    toggle.setAttribute("aria-label", todo.done ? "Mark TODO unresolved" : "Mark TODO resolved");
    toggle.addEventListener("click", () => void mutateTodo({ action: "toggle", todo_id: todo.id, done: !todo.done }));
    const todoText = createElement("p", "todo-item-text", todo.text);
    const textIsLong = todo.text.length > 320;
    if (textIsLong) todoText.classList.add("collapsible");
    main.append(toggle, todoText);
    item.append(main);
    const metaParts = [];
    if (todo.work_state) metaParts.push(todo.work_state);
    if (todo.work_provider && todo.work_provider !== "unknown") metaParts.push(todo.work_provider);
    if (todo.attachment_count) metaParts.push(`${todo.attachment_count} attachment(s)`);
    if (metaParts.length) item.append(createElement("p", "todo-item-meta", metaParts.join(" · ")));
    const actions = createElement("div", "todo-item-actions");
    if (textIsLong) {
      actions.append(createExpansionButton(todoText, "Show more", "Show less"));
    }
    if (writeEnabled) {
      const edit = createElement("button", "panel-action-button", "Edit");
      edit.type = "button";
      edit.addEventListener("click", () => {
        const panel = elements.quickPanelContent.querySelector(".todo-panel");
        const current = panel?.querySelector(".todo-editor-well");
        if (current) current.replaceWith(createTodoEditor(todo));
      });
      const remove = createElement("button", "panel-action-button danger", "Delete");
      remove.type = "button";
      remove.addEventListener("click", () => {
        if (window.confirm("Delete this TODO?")) void mutateTodo({ action: "delete", todo_id: todo.id });
      });
      actions.append(edit, remove);
    }
    if (actions.childElementCount) item.append(actions);
    return item;
  }

  function createExpansionButton(target, collapsedLabel, expandedLabel) {
    const button = createElement("button", "panel-action-button", collapsedLabel);
    button.type = "button";
    button.setAttribute("aria-expanded", "false");
    button.addEventListener("click", () => {
      const expanded = target.classList.toggle("expanded");
      button.textContent = expanded ? expandedLabel : collapsedLabel;
      button.setAttribute("aria-expanded", String(expanded));
    });
    return button;
  }

  async function mutateTodo(action) {
    if (!state.selectedPath || state.quickPanel !== "todo") return;
    setQuickPanelStatus("Applying TODO change");
    setPanelBusy(true);
    try {
      const surface = await postJSON("/api/mobile/projects/todos/action", {
        project_path: state.selectedPath,
        request_id: mobileRequestID(),
        ...action,
      });
      if (state.quickPanel !== "todo") return;
      renderTodoPanel(surface);
      await refreshSelectedProjectContext();
    } catch (error) {
      if (isAuthRequiredError(error)) return;
      setQuickPanelStatus(error.message || "Could not change TODO", "warning");
    } finally {
      setPanelBusy(false);
    }
  }

  async function loadRuntimePanel() {
    if (!state.selectedPath) {
      renderPanelError(new Error("Select a project first."));
      return;
    }
    const requestID = ++state.quickPanelRequestID;
    renderPanelLoading("Scanning project runtime");
    try {
      const surface = await fetchJSON(`/api/mobile/projects/runtime?path=${encodeURIComponent(state.selectedPath)}`);
      if (requestID !== state.quickPanelRequestID || state.quickPanel !== "runtime") return;
      renderRuntimePanel(surface);
    } catch (error) {
      if (isAuthRequiredError(error) || requestID !== state.quickPanelRequestID) return;
      renderPanelError(error, () => void loadRuntimePanel());
    }
  }

  function renderRuntimePanel(surface) {
    elements.quickPanelTitle.textContent = surface.project?.name ? `${surface.project.name} runtime` : "Runtime";
    setQuickPanelStatus(surface.write_enabled
      ? `Scanned ${formatClockTime(new Date(surface.updated_at))} · phone control armed`
      : "Runtime is read only · enable Phone control in Mobile settings");
    const root = createElement("div", "runtime-panel");
    const toolbar = createElement("div", "runtime-toolbar");
    const running = (surface.processes || []).filter((process) => process.running).length;
    toolbar.append(createElement("span", "", `${running} running · ${(surface.processes || []).length} detected`));
    toolbar.append(createElement("span", "", surface.run_command ? "Command ready" : "No run command"));
    root.append(toolbar);

    const commandWell = createElement("form", "runtime-command-well");
    const commandRow = createElement("div", "runtime-command-row");
    const commandInput = document.createElement("input");
    commandInput.className = "runtime-command-input";
    commandInput.type = "text";
    commandInput.maxLength = 2000;
    commandInput.value = surface.run_command || "";
    commandInput.placeholder = "Project run command";
    commandInput.setAttribute("aria-label", "Runtime command");
    commandInput.disabled = !surface.write_enabled;
    const start = createElement("button", "runtime-main-action", "Start");
    start.type = "submit";
    start.disabled = !surface.write_enabled || commandInput.value.trim() === "";
    commandInput.addEventListener("input", () => {
      start.disabled = !surface.write_enabled || commandInput.value.trim() === "";
    });
    commandWell.addEventListener("submit", (event) => {
      event.preventDefault();
      void mutateRuntime({ action: "start", command: commandInput.value.trim() });
    });
    commandRow.append(commandInput, start);
    commandWell.append(commandRow);
    root.append(commandWell);

    const list = createElement("div", "runtime-list");
    if (!(surface.processes || []).length) {
      list.append(createElement("p", "panel-empty", "No managed runtime or project-local listener is active."));
    } else {
      for (const process of surface.processes) list.append(createRuntimeProcess(process));
    }
    root.append(list);
    if ((surface.warnings || []).length) {
      const warningSection = {
        id: "warnings",
        title: "Process warnings",
        summary: `${surface.warnings.length} warning(s)`,
        blocks: surface.warnings.map((warning) => ({ kind: "bullet", text: warning, tone: "warning" })),
      };
      root.append(createPanelSection(warningSection, 1));
    }
    elements.quickPanelContent.replaceChildren(root);
  }

  function createRuntimeProcess(process) {
    const card = createElement("article", "runtime-process");
    const head = createElement("div", "runtime-process-head");
    const lampClass = process.status?.tone === "danger" ? "red" : process.running ? "green" : "amber dim";
    head.append(createElement("span", `lamp ${lampClass}`));
    head.append(createElement("strong", "", process.name || "Runtime"));
    const status = createElement("span", "metal-status", process.status?.label || "Unknown");
    const semanticClass = toneClass(process.status?.tone);
    if (semanticClass) status.classList.add(semanticClass);
    head.append(status);
    card.append(head);
    let commandToggle = null;
    if (process.command) {
      const command = createElement("p", "runtime-command", process.command);
      if (process.command.length > 280) {
        command.classList.add("collapsible");
        commandToggle = createExpansionButton(command, "Show command", "Hide command");
      }
      card.append(command);
    }
    const meta = [];
    if (process.pid) meta.push(`PID ${process.pid}`);
    if ((process.ports || []).length) meta.push(`ports ${process.ports.join(", ")}`);
    meta.push(process.managed ? "managed" : "external");
    card.append(createElement("p", "runtime-process-meta", meta.join(" · ")));
    if ((process.recent_output || []).length) {
      const output = createElement("details", "panel-section");
      const summary = document.createElement("summary");
      summary.append(createElement("span", "", "Recent output"));
      const body = createElement("div", "panel-section-body");
      body.append(createElement("pre", "runtime-output", process.recent_output.join("\n")));
      output.append(summary, body);
      card.append(output);
    }
    const actions = createElement("div", "runtime-process-actions");
    if (commandToggle) actions.append(commandToggle);
    for (const rawURL of process.urls || []) {
      const url = runtimeURLForPhone(rawURL);
      const open = createElement("a", "panel-action-button", "Open");
      open.href = url;
      open.target = "_blank";
      open.rel = "noopener noreferrer";
      actions.append(open);
    }
    if (process.can_restart) {
      const restart = createElement("button", "panel-action-button", "Restart");
      restart.type = "button";
      restart.addEventListener("click", () => {
        if (window.confirm(`Restart ${process.name}?`)) {
          void mutateRuntime({ action: "restart", process_id: process.id, command: process.command });
        }
      });
      actions.append(restart);
    }
    if (process.can_stop) {
      const stop = createElement("button", "panel-action-button danger", "Stop");
      stop.type = "button";
      stop.addEventListener("click", () => {
        if (window.confirm(`Stop ${process.name}?`)) void mutateRuntime({ action: "stop", process_id: process.id });
      });
      actions.append(stop);
    }
    if (actions.childElementCount) card.append(actions);
    return card;
  }

  function runtimeURLForPhone(rawURL) {
    try {
      const parsed = new URL(rawURL);
      if (["127.0.0.1", "localhost", "::1", "[::1]"].includes(parsed.hostname)) {
        parsed.hostname = window.location.hostname;
      }
      return parsed.href;
    } catch (_error) {
      return rawURL;
    }
  }

  async function mutateRuntime(action) {
    if (!state.selectedPath || state.quickPanel !== "runtime") return;
    setQuickPanelStatus(`${action.action === "stop" ? "Stopping" : "Starting"} runtime`);
    setPanelBusy(true);
    try {
      const surface = await postJSON("/api/mobile/projects/runtime/action", {
        project_path: state.selectedPath,
        request_id: mobileRequestID(),
        ...action,
      });
      if (state.quickPanel === "runtime") renderRuntimePanel(surface);
    } catch (error) {
      if (isAuthRequiredError(error)) return;
      setQuickPanelStatus(error.message || "Could not control runtime", "warning");
    } finally {
      setPanelBusy(false);
    }
  }

  function setPanelBusy(busy) {
    for (const control of elements.quickPanelContent.querySelectorAll("button, input, textarea")) {
      if (busy) {
        control.dataset.wasDisabled = String(control.disabled);
        control.disabled = true;
      } else if (control.dataset.wasDisabled !== undefined) {
        control.disabled = control.dataset.wasDisabled === "true";
        delete control.dataset.wasDisabled;
      }
    }
  }

  function renderCommandPanel() {
    const consoleNode = createElement("form", "command-console");
    const well = createElement("div", "command-input-well");
    const row = createElement("div", "command-input-row");
    const input = document.createElement("input");
    input.className = "command-input";
    input.type = "text";
    input.autocomplete = "off";
    input.autocapitalize = "off";
    input.spellcheck = false;
    input.value = "/";
    input.maxLength = 2000;
    input.setAttribute("aria-label", "Slash command");
    const run = createElement("button", "command-run-button", "Run");
    run.type = "submit";
    const suggestions = createElement("div", "command-suggestions");
    suggestions.setAttribute("role", "listbox");
    input.addEventListener("input", () => void loadCommandSuggestions(input, suggestions));
    consoleNode.addEventListener("submit", (event) => {
      event.preventDefault();
      void executeCommand(input.value, input, suggestions);
    });
    row.append(input, run);
    well.append(row);
    consoleNode.append(well, suggestions);
    elements.quickPanelContent.replaceChildren(consoleNode);
    setQuickPanelStatus(state.selectedPath
      ? `Commands target ${projectNameForPath(state.selectedPath)}`
      : "Dashboard command palette");
    void loadCommandSuggestions(input, suggestions);
    window.requestAnimationFrame(() => {
      input.focus();
      input.setSelectionRange(input.value.length, input.value.length);
    });
  }

  async function loadCommandSuggestions(input, container) {
    const requestID = ++state.commandSuggestionRequestID;
    const query = input.value.trimStart() || "/";
    const live = currentSessionIsLive();
    const params = new URLSearchParams({ q: query, context: live ? "session" : "dashboard" });
    if (state.selectedPath) params.set("path", state.selectedPath);
    try {
      const surface = await fetchJSON(`/api/mobile/commands/suggestions?${params.toString()}`);
      if (requestID !== state.commandSuggestionRequestID || state.quickPanel !== "command") return;
      container.replaceChildren();
      const suggestions = surface.suggestions || [];
      if (!suggestions.length) {
        container.append(createElement("p", "panel-empty", "No matching slash commands."));
        return;
      }
      for (const suggestion of suggestions.slice(0, 18)) {
        const button = createElement("button", "command-suggestion");
        button.type = "button";
        button.disabled = !suggestion.supported;
        button.setAttribute("role", "option");
        button.append(createElement("strong", "", suggestion.display || suggestion.insert));
        button.append(createElement("span", "command-source", suggestion.source || "command"));
        button.append(createElement("small", "", suggestion.supported
          ? suggestion.summary
          : `${suggestion.summary} · ${suggestion.disabled_reason || "Unavailable on mobile"}`));
        button.addEventListener("click", () => {
          input.value = suggestion.insert;
          input.focus();
          void loadCommandSuggestions(input, container);
        });
        container.append(button);
      }
    } catch (error) {
      if (isAuthRequiredError(error) || requestID !== state.commandSuggestionRequestID) return;
      container.replaceChildren(createElement("p", "panel-empty", error.message || "Could not load commands"));
    }
  }

  async function executeCommand(raw, input, suggestions) {
    const command = raw.trim();
    if (!command.startsWith("/")) {
      setQuickPanelStatus("Enter a slash command", "warning");
      return;
    }
    const localAction = new Map([
      ["/session", "session"],
      ["/sidebar", "session-panel"],
      ["/details", "details"],
      ["/todo", "todo"],
      ["/runtime", "runtime"],
    ]).get(command.toLowerCase());
    if (localAction) {
      if (!state.selectedPath) {
        setQuickPanelStatus("Select a project first", "warning");
        return;
      }
      if (localAction === "session") {
        closeQuickPanel();
      } else {
        openQuickPanel(localAction === "session-panel" ? "session" : localAction);
      }
      return;
    }
    setQuickPanelStatus(`Running ${command}`);
    setPanelBusy(true);
    try {
      const result = await postJSON("/api/mobile/commands/execute", {
        project_path: state.selectedPath,
        session_id: currentSessionIsLive() ? state.selectedSessionID : "",
        request_id: mobileRequestID(),
        command,
      });
      if (result.client_action && ["details", "todo", "runtime"].includes(result.client_action)) {
        openQuickPanel(result.client_action);
        return;
      }
      setQuickPanelStatus(result.status || `${command} complete`);
      if (command === "/refresh") await loadDashboard(false);
      if (result.client_action === "session") {
        closeQuickPanel();
        if (state.selectedSessionID) await loadSessionDetail(state.selectedSessionID, false);
      } else {
        input.value = command;
        void loadCommandSuggestions(input, suggestions);
      }
    } catch (error) {
      if (isAuthRequiredError(error)) return;
      setQuickPanelStatus(error.message || `Could not run ${command}`, "warning");
    } finally {
      setPanelBusy(false);
    }
  }

  function currentSessionIsLive() {
    return Boolean(state.selectedSessionID && (state.sessionLive
      || state.selectedProjectSessions.some((session) => session.id === state.selectedSessionID && session.live)));
  }

  async function refreshSelectedProjectContext() {
    if (!state.selectedPath) return;
    try {
      const detail = await fetchJSON(`/api/mobile/projects/detail?path=${encodeURIComponent(state.selectedPath)}`);
      if (detail?.project?.path !== state.selectedPath) return;
      state.projectDetailCache.set(state.selectedPath, detail);
      state.selectedProjectDetail = detail;
      renderSessionProjectContext(detail);
    } catch (error) {
      if (!isAuthRequiredError(error)) setQuickPanelStatus("Change saved; summary refresh is pending");
    }
  }

  function closeSessionStream() {
    if (state.sessionStream) state.sessionStream.close();
    state.sessionStream = null;
    state.sessionStreamKey = "";
    state.sessionStreamConnected = false;
  }

  function connectSessionStream(session) {
    if (!session?.live || !session.id || !session.project_path || typeof window.EventSource !== "function") {
      closeSessionStream();
      return;
    }
    const key = `${session.project_path}\n${session.id}`;
    if (state.sessionStream && state.sessionStreamKey === key) return;

    closeSessionStream();
    const source = new window.EventSource(
      `/api/mobile/sessions/stream?path=${encodeURIComponent(session.project_path)}&session_id=${encodeURIComponent(session.id)}`,
    );
    state.sessionStream = source;
    state.sessionStreamKey = key;

    source.addEventListener("open", () => {
      if (state.sessionStream !== source) return;
      state.sessionStreamConnected = true;
      elements.sessionUpdatedLabel.textContent = "Streaming live";
    });
    source.addEventListener("session", (event) => {
      if (state.sessionStream !== source) return;
      let detail;
      try {
        detail = JSON.parse(event.data);
      } catch (_error) {
        return;
      }
      if (state.selectedPath !== session.project_path || state.selectedSessionID !== session.id) return;
      if (sessionDetailIsOlder(detail)) return;
      state.sessionStreamConnected = true;
      const signature = sessionDetailSignature(detail);
      if (signature === state.sessionDetailSignature) return;
      state.sessionDetailSignature = signature;
      renderSessionDetail(detail, false);
    });
    source.addEventListener("error", () => {
      if (state.sessionStream !== source) return;
      state.sessionStreamConnected = false;
      if (state.selectedSessionID === session.id) elements.sessionUpdatedLabel.textContent = "Stream reconnecting";
    });
    const finishStream = (event) => {
      if (state.sessionStream !== source) return;
      closeSessionStream();
      if (state.selectedSessionID !== session.id) return;
      elements.sessionUpdatedLabel.textContent = event.type === "replaced" ? "Session changed" : "Live session ended";
      void loadProjectSessions(session.project_path, false);
    };
    source.addEventListener("end", finishStream);
    source.addEventListener("replaced", finishStream);
  }

  function sessionDetailIsOlder(detail) {
    const revision = Number(detail?.session?.transcript_revision || 0);
    return revision > 0 && revision < state.sessionTranscriptRevision;
  }

  async function openSession(sessionID, updateHistory) {
    if (!sessionID || !state.selectedPath) return;
    state.selectedSessionID = sessionID;
    state.sessionDetailSignature = "";
    state.sessionTranscriptRevision = 0;
    state.sessionEntries = [];
    state.sessionEmptyMessage = "";
    state.sessionLastEntryKey = "";
    state.sessionHasNewActivity = false;
    state.sessionStickToBottom = true;
    state.sessionInput = null;
    state.sessionSubmitting = false;
    state.sessionFeedback = "";
    state.sessionLive = false;
    elements.body.classList.add("detail-open", "session-open");
    elements.sessionView.hidden = false;
    elements.sessionView.removeAttribute("aria-hidden");
    elements.sessionContent.hidden = true;
    elements.sessionStandby.hidden = true;
    elements.sessionActionDeck.hidden = false;
    renderSessionProjectContext(state.projectDetailCache.get(state.selectedPath) || state.selectedProjectDetail);
    elements.sessionState.replaceChildren(createElement("p", "", "Tuning engineer channel"));
    elements.sessionProjectName.textContent = projectNameForPath(state.selectedPath);
    if (updateHistory) {
      window.history.pushState(
        { projectPath: state.selectedPath, sessionID },
        "",
        `#session=${encodeURIComponent(sessionID)}&project=${encodeURIComponent(state.selectedPath)}`,
      );
    }
    await loadSessionDetail(sessionID, true);
  }

  async function loadSessionDetail(sessionID, initial = false) {
    const projectPath = state.selectedPath;
    if (!projectPath || !sessionID) return;
    const requestID = ++state.sessionRequestID;
    try {
      const detail = await fetchJSON(
        `/api/mobile/sessions/detail?path=${encodeURIComponent(projectPath)}&session_id=${encodeURIComponent(sessionID)}`,
      );
      if (requestID !== state.sessionRequestID || state.selectedSessionID !== sessionID || state.selectedPath !== projectPath) return;
      if (sessionDetailIsOlder(detail)) return;
      const signature = sessionDetailSignature(detail);
      if (!initial && signature === state.sessionDetailSignature) return;
      state.sessionDetailSignature = signature;
      renderSessionDetail(detail, initial);
    } catch (error) {
      if (isAuthRequiredError(error)) return;
      if (requestID !== state.sessionRequestID || state.selectedSessionID !== sessionID) return;
      if (!elements.sessionContent.hidden) {
        elements.sessionUpdatedLabel.textContent = "Link error";
        return;
      }
      const message = createElement("p", "", `Could not load session: ${error.message}`);
      const retry = createElement("button", "error-action", "Try again");
      retry.type = "button";
      retry.addEventListener("click", () => loadSessionDetail(state.selectedSessionID, true));
      elements.sessionState.replaceChildren(message, retry);
    }
  }

  function sessionDetailSignature(detail) {
    const entries = detail.entries || [];
    const last = entries.at(-1) || {};
    return JSON.stringify([
      detail.session?.id,
      detail.session?.transcript_revision,
      detail.session?.status?.label,
      detail.input,
      entries.length,
      last.item_id,
      last.text,
      detail.truncated,
    ]);
  }

  function renderSessionDetail(detail, initial) {
    const session = detail.session;
    state.sessionLive = Boolean(session.live);
    const transcript = elements.sessionTranscript;
    const wasNearBottom = state.sessionStickToBottom
      || transcript.scrollHeight - transcript.scrollTop - transcript.clientHeight < 72;
    const entries = detail.entries || [];
    const nextLastEntryKey = sessionEntryKey(entries.at(-1));
    const hasNewActivity = !initial
      && state.sessionLastEntryKey !== ""
      && nextLastEntryKey !== state.sessionLastEntryKey;
    state.sessionEntries = entries;
    state.sessionEmptyMessage = detail.empty_message || "";
    state.sessionLastEntryKey = nextLastEntryKey;
    state.sessionTranscriptRevision = Math.max(state.sessionTranscriptRevision, Number(session.transcript_revision || 0));

    elements.sessionState.replaceChildren();
    elements.sessionContent.hidden = false;
    elements.sessionProjectName.textContent = projectNameForPath(session.project_path);
    elements.sessionProvider.textContent = session.live ? `${session.provider_label} live` : session.provider_label;
    elements.sessionTitle.textContent = `${session.provider_label} session`;
    elements.sessionSummary.textContent = session.summary;
    elements.sessionStatus.textContent = session.status.label;
    elements.sessionStatus.className = "metal-status";
    const semanticClass = toneClass(session.status.tone);
    if (semanticClass) elements.sessionStatus.classList.add(semanticClass);
    elements.sessionID.textContent = session.display_id;
    elements.sessionLiveLamp.className = `lamp ${sessionLampClass(session)}`;
    elements.sessionUpdatedLabel.textContent = session.live
      ? `${state.sessionStreamConnected ? "Streaming" : "Live"} ${session.last_activity_label}`
      : `Updated ${session.last_activity_label}`;
    elements.sessionTruncated.hidden = !detail.truncated;

    renderSessionInstruments(detail.instruments || [], session);
    renderSessionTranscript();
    renderSessionComposer(detail);
    updateTranscriptControls();
    if (session.live) {
      connectSessionStream(session);
    } else {
      closeSessionStream();
    }

    document.title = `${session.provider_label} - ${projectNameForPath(session.project_path)} - Little Control Room`;
    if (initial) window.scrollTo({ top: 0, behavior: "auto" });
    if (initial || wasNearBottom) {
      window.requestAnimationFrame(() => {
        scrollSessionToLatest();
      });
    } else if (hasNewActivity) {
      state.sessionHasNewActivity = true;
      updateTranscriptControls();
    }
  }

  function sessionEntryKey(entry) {
    if (!entry) return "";
    return JSON.stringify([entry.item_id, entry.kind, entry.text]);
  }

  function renderSessionInstruments(instruments, session) {
    elements.sessionInstrumentList.replaceChildren();
    for (const instrument of instruments) {
      const row = createElement("div", "session-instrument-row");
      row.append(createElement("span", "session-instrument-label", instrument.label));
      const value = createElement("span", "session-instrument-value", instrument.text);
      const semanticClass = toneClass(instrument.tone);
      if (semanticClass) value.classList.add(semanticClass);
      row.append(value);
      elements.sessionInstrumentList.append(row);
    }
    elements.sessionInstrumentSummary.textContent = [session.model, session.status.label].filter(Boolean).join(" / ") || "Session readout";
  }

  function renderSessionTranscript() {
    const entries = state.transcriptMode === "all"
      ? state.sessionEntries
      : state.sessionEntries.filter((entry) => ["user", "agent", "plan", "error"].includes(entry.kind));
    if (entries.length === 0) {
      const emptyMessage = state.transcriptMode === "conversation" && state.sessionEntries.length > 0
        ? "No conversation entries yet"
        : state.sessionEmptyMessage || "No transcript activity";
      const existingEmpty = elements.sessionTranscript.querySelector(":scope > .transcript-empty");
      if (existingEmpty && existingEmpty.textContent === emptyMessage && elements.sessionTranscript.childElementCount === 1) return;
      elements.sessionTranscript.replaceChildren(createElement("p", "transcript-empty", emptyMessage));
      return;
    }

    const existing = new Map();
    for (const node of elements.sessionTranscript.children) {
      if (node.dataset.entryKey) existing.set(node.dataset.entryKey, node);
    }
    const occurrences = new Map();
    const nextNodes = [];
    entries.forEach((entry, index) => {
      const baseKey = entry.item_id ? `${entry.kind}:${entry.item_id}` : `${entry.kind}:position-${index}`;
      const occurrence = occurrences.get(baseKey) || 0;
      occurrences.set(baseKey, occurrence + 1);
      const key = `${baseKey}:${occurrence}`;
      const signature = JSON.stringify([entry.kind, entry.label, entry.tone, entry.text]);
      let node = existing.get(key);
      if (!node || node.dataset.entrySignature !== signature) {
        node = createTranscriptEntry(entry);
        node.dataset.entryKey = key;
        node.dataset.entrySignature = signature;
      }
      nextNodes.push(node);
    });
    reconcileTranscriptNodes(nextNodes);
  }

  function reconcileTranscriptNodes(nextNodes) {
    const keep = new Set(nextNodes);
    for (const node of [...elements.sessionTranscript.children]) {
      if (!keep.has(node)) node.remove();
    }
    nextNodes.forEach((node, index) => {
      const current = elements.sessionTranscript.children[index];
      if (current !== node) elements.sessionTranscript.insertBefore(node, current || null);
    });
  }

  function sessionStreamAvailable() {
    return Boolean(state.sessionStream && state.sessionStreamConnected);
  }

  function refreshSelectedSessionFallback() {
    if (!state.selectedSessionID || sessionStreamAvailable()) return;
    void loadSessionDetail(state.selectedSessionID, false);
  }

  function refreshSelectedProjectSessions() {
    if (state.selectedSessionID) {
      refreshSelectedSessionFallback();
    } else if (state.selectedPath) {
      void loadProjectSessions(state.selectedPath, false);
    }
  }

  function renderSessionComposer(detail) {
    const session = detail.session || {};
    const input = detail.input || {};
    state.sessionInput = input;
    const liveSurface = Boolean(session.live);
    const inputEnabled = Boolean(liveSurface && input.enabled);
    elements.sessionContent.classList.toggle("has-composer", liveSurface);
    elements.sessionContent.classList.toggle("input-enabled", inputEnabled);
    elements.sessionComposer.hidden = !liveSurface;
    elements.sessionReadonlyStrip.hidden = liveSurface;

    if (!liveSurface) {
      elements.sessionReadonlyLabel.textContent = "Recorded transcript — no live session to message";
      elements.sessionLinkLabel.textContent = "Stored";
      return;
    }

    const sessionChanged = elements.sessionMessage.dataset.sessionId !== session.id;
    if (sessionChanged) {
      elements.sessionMessage.dataset.sessionId = session.id;
      elements.sessionMessage.value = loadSessionDraft(session.id);
      resizeSessionMessage();
    }

    const available = Boolean(inputEnabled && input.available && !state.sessionSubmitting);
    const modeLabel = input.label || "Send";
    elements.sessionComposer.classList.toggle("input-unavailable", !available);
    elements.sessionComposerLamp.className = `lamp ${inputEnabled && input.available ? "cyan" : "amber"}`;
    elements.sessionComposerState.textContent = state.sessionSubmitting
      ? "Transmitting"
      : inputEnabled && input.available
        ? "Live session input"
        : input.reason || "Live input unavailable";
    elements.sessionComposerMode.textContent = modeLabel;
    elements.sessionSendButton.textContent = state.sessionSubmitting ? "Sending" : modeLabel;
    elements.sessionMessage.disabled = !available;
    elements.sessionMessage.placeholder = inputEnabled ? "Message the engineer" : "Messaging is off";
    elements.sessionSendButton.disabled = !available || elements.sessionMessage.value.trim() === "";
    elements.sessionComposerFeedback.textContent = state.sessionFeedback || (inputEnabled
      ? ""
      : "Enable Session messages in Little Control Room’s Mobile settings to reply from this phone.");
  }

  function sessionDraftKey(sessionID) {
    return `lcr.mobile.session-draft.${sessionID}`;
  }

  function loadSessionDraft(sessionID) {
    if (!sessionID) return "";
    return window.localStorage.getItem(sessionDraftKey(sessionID)) || "";
  }

  function persistSessionDraft() {
    const sessionID = elements.sessionMessage.dataset.sessionId;
    if (!sessionID) return;
    const value = elements.sessionMessage.value;
    if (value) {
      window.localStorage.setItem(sessionDraftKey(sessionID), value);
    } else {
      window.localStorage.removeItem(sessionDraftKey(sessionID));
    }
  }

  function resizeSessionMessage() {
    elements.sessionMessage.style.height = "auto";
    elements.sessionMessage.style.height = `${Math.min(elements.sessionMessage.scrollHeight, 144)}px`;
  }

  function mobileRequestID() {
    const random = new Uint32Array(2);
    if (window.crypto?.getRandomValues) window.crypto.getRandomValues(random);
    return `${Date.now().toString(36)}-${random[0].toString(36)}${random[1].toString(36)}`;
  }

  async function submitSessionMessage() {
    const text = elements.sessionMessage.value.trim();
    if (!text || state.sessionSubmitting || !state.sessionInput?.available || !state.selectedPath || !state.selectedSessionID) return;
    state.sessionSubmitting = true;
    state.sessionFeedback = "";
    renderSessionComposer({
      session: { id: state.selectedSessionID, live: true },
      input: state.sessionInput,
    });
    try {
      const result = await postJSON("/api/mobile/sessions/input", {
        project_path: state.selectedPath,
        session_id: state.selectedSessionID,
        request_id: mobileRequestID(),
        text,
      });
      elements.sessionMessage.value = "";
      persistSessionDraft();
      resizeSessionMessage();
      state.sessionFeedback = result.status || "Message sent";
      state.sessionStickToBottom = true;
      await loadSessionDetail(state.selectedSessionID, true);
    } catch (error) {
      if (isAuthRequiredError(error)) return;
      state.sessionFeedback = error.message || "Could not send the message";
    } finally {
      state.sessionSubmitting = false;
      if (state.selectedSessionID) {
        renderSessionComposer({
          session: { id: state.selectedSessionID, live: true },
          input: state.sessionInput || {},
        });
      }
    }
  }

  function updateTranscriptControls() {
    for (const button of elements.transcriptMode.querySelectorAll("button")) {
      const selected = button.dataset.transcriptMode === state.transcriptMode;
      button.classList.toggle("selected", selected);
      button.setAttribute("aria-pressed", String(selected));
    }
    elements.sessionFollowButton.hidden = !state.sessionHasNewActivity;
  }

  function scrollSessionToLatest() {
    elements.sessionTranscript.scrollTop = elements.sessionTranscript.scrollHeight;
    state.sessionStickToBottom = true;
    state.sessionHasNewActivity = false;
    updateTranscriptControls();
  }

  function createTranscriptEntry(entry) {
    if (entry.kind === "reasoning") {
      const details = createElement("details", "transcript-entry transcript-reasoning");
      const summary = createElement("summary", "transcript-entry-label", entry.label);
      details.append(summary, createMarkdownBody(entry.text));
      return details;
    }

    const node = createElement("article", `transcript-entry transcript-${entry.kind}`);
    const label = createElement("div", "transcript-entry-label", entry.label);
    const semanticClass = toneClass(entry.tone);
    if (semanticClass) label.classList.add(semanticClass);
    node.append(label, createMarkdownBody(entry.text));
    return node;
  }

  function createMarkdownBody(source) {
    const body = createElement("div", "transcript-entry-text markdown-body");
    renderMarkdownBlocks(body, String(source || "").replaceAll("\r\n", "\n"));
    return body;
  }

  function renderMarkdownBlocks(container, source) {
    const lines = source.split("\n");
    let index = 0;
    while (index < lines.length) {
      const line = lines[index];
      if (line.trim() === "") {
        index++;
        continue;
      }

      const fence = line.match(/^\s*```([^`]*)$/);
      if (fence) {
        const codeLines = [];
        index++;
        while (index < lines.length && !/^\s*```\s*$/.test(lines[index])) {
          codeLines.push(lines[index]);
          index++;
        }
        if (index < lines.length) index++;
        const pre = document.createElement("pre");
        const code = document.createElement("code");
        const language = fence[1].trim().split(/\s+/, 1)[0];
        if (language) code.className = `language-${language.replace(/[^a-z0-9_+-]/gi, "")}`;
        code.textContent = codeLines.join("\n");
        pre.append(code);
        container.append(pre);
        continue;
      }

      const heading = line.match(/^\s{0,3}(#{1,6})\s+(.+)$/);
      if (heading) {
        const node = document.createElement(`h${heading[1].length}`);
        appendInlineMarkdown(node, heading[2].replace(/\s+#+\s*$/, ""));
        container.append(node);
        index++;
        continue;
      }

      if (index + 1 < lines.length && isMarkdownTableHeader(line, lines[index + 1])) {
        const headerCells = splitMarkdownTableRow(line);
        const alignments = splitMarkdownTableRow(lines[index + 1]).map(markdownTableAlignment);
        const table = document.createElement("table");
        const head = document.createElement("thead");
        const headRow = document.createElement("tr");
        for (let cellIndex = 0; cellIndex < headerCells.length; cellIndex++) {
          const cell = document.createElement("th");
          if (alignments[cellIndex]) cell.classList.add(`markdown-align-${alignments[cellIndex]}`);
          appendInlineMarkdown(cell, headerCells[cellIndex]);
          headRow.append(cell);
        }
        head.append(headRow);
        table.append(head);
        index += 2;
        const tableBody = document.createElement("tbody");
        while (index < lines.length && lines[index].includes("|") && lines[index].trim() !== "") {
          const row = document.createElement("tr");
          const cells = splitMarkdownTableRow(lines[index]);
          for (let cellIndex = 0; cellIndex < headerCells.length; cellIndex++) {
            const cell = document.createElement("td");
            if (alignments[cellIndex]) cell.classList.add(`markdown-align-${alignments[cellIndex]}`);
            appendInlineMarkdown(cell, cells[cellIndex] || "");
            row.append(cell);
          }
          tableBody.append(row);
          index++;
        }
        table.append(tableBody);
        const scroller = createElement("div", "markdown-table-scroll");
        scroller.append(table);
        container.append(scroller);
        continue;
      }

      if (/^\s{0,3}>/.test(line)) {
        const quoteLines = [];
        while (index < lines.length && /^\s{0,3}>/.test(lines[index])) {
          quoteLines.push(lines[index].replace(/^\s{0,3}>\s?/, ""));
          index++;
        }
        const quote = document.createElement("blockquote");
        renderMarkdownBlocks(quote, quoteLines.join("\n"));
        container.append(quote);
        continue;
      }

      if (/^\s{0,3}((\*\s*){3,}|(-\s*){3,}|(_\s*){3,})$/.test(line)) {
        container.append(document.createElement("hr"));
        index++;
        continue;
      }

      const listMatch = line.match(/^\s{0,3}([-+*]|\d+[.)])\s+(.+)$/);
      if (listMatch) {
        const ordered = /^\d/.test(listMatch[1]);
        const list = document.createElement(ordered ? "ol" : "ul");
        while (index < lines.length) {
          const itemMatch = lines[index].match(/^\s{0,3}([-+*]|\d+[.)])\s+(.+)$/);
          if (!itemMatch || /^\d/.test(itemMatch[1]) !== ordered) break;
          const item = document.createElement("li");
          const task = itemMatch[2].match(/^\[([ xX])\]\s+(.+)$/);
          if (task) {
            const checkbox = document.createElement("input");
            checkbox.type = "checkbox";
            checkbox.checked = task[1].toLowerCase() === "x";
            checkbox.disabled = true;
            item.append(checkbox, document.createTextNode(" "));
            appendInlineMarkdown(item, task[2]);
          } else {
            appendInlineMarkdown(item, itemMatch[2]);
          }
          list.append(item);
          index++;
        }
        container.append(list);
        continue;
      }

      const paragraphLines = [line];
      index++;
      while (index < lines.length && lines[index].trim() !== "" && !startsMarkdownBlock(lines, index)) {
        paragraphLines.push(lines[index]);
        index++;
      }
      const paragraph = document.createElement("p");
      paragraphLines.forEach((paragraphLine, lineIndex) => {
        const hardBreak = /(?: {2,}|\\)$/.test(paragraphLine);
        appendInlineMarkdown(paragraph, paragraphLine.replace(/(?: {2,}|\\)$/, ""));
        if (lineIndex < paragraphLines.length - 1) {
          paragraph.append(hardBreak ? document.createElement("br") : document.createTextNode(" "));
        }
      });
      container.append(paragraph);
    }
  }

  function startsMarkdownBlock(lines, index) {
    const line = lines[index];
    return /^\s*```/.test(line)
      || /^\s{0,3}#{1,6}\s+/.test(line)
      || /^\s{0,3}>/.test(line)
      || /^\s{0,3}([-+*]|\d+[.)])\s+/.test(line)
      || /^\s{0,3}((\*\s*){3,}|(-\s*){3,}|(_\s*){3,})$/.test(line)
      || (index + 1 < lines.length && isMarkdownTableHeader(line, lines[index + 1]));
  }

  function isMarkdownTableHeader(header, separator) {
    if (!header.includes("|")) return false;
    const cells = splitMarkdownTableRow(separator);
    return cells.length > 0 && cells.every((cell) => /^:?-{3,}:?$/.test(cell.trim()));
  }

  function splitMarkdownTableRow(row) {
    const trimmed = row.trim().replace(/^\|/, "").replace(/\|$/, "");
    const cells = [];
    let cell = "";
    let escaped = false;
    let inCode = false;
    for (const char of trimmed) {
      if (escaped) {
        cell += char;
        escaped = false;
      } else if (char === "\\") {
        escaped = true;
        cell += char;
      } else if (char === "`") {
        inCode = !inCode;
        cell += char;
      } else if (char === "|" && !inCode) {
        cells.push(cell.trim());
        cell = "";
      } else {
        cell += char;
      }
    }
    cells.push(cell.trim());
    return cells;
  }

  function markdownTableAlignment(cell) {
    const value = cell.trim();
    if (value.startsWith(":") && value.endsWith(":")) return "center";
    if (value.endsWith(":")) return "right";
    return "";
  }

  function appendInlineMarkdown(container, source) {
    let cursor = 0;
    while (cursor < source.length) {
      const token = nextInlineMarkdownToken(source, cursor);
      if (!token) {
        appendMarkdownText(container, source.slice(cursor));
        return;
      }
      appendMarkdownText(container, source.slice(cursor, token.start));
      const node = document.createElement(token.tag);
      if (token.tag === "code") {
        node.textContent = token.content;
      } else if (token.tag === "a") {
        node.href = token.href;
        node.target = "_blank";
        node.rel = "noopener noreferrer";
        appendInlineMarkdown(node, token.content);
      } else {
        appendInlineMarkdown(node, token.content);
      }
      container.append(node);
      cursor = token.end;
    }
  }

  function nextInlineMarkdownToken(source, start) {
    for (let index = start; index < source.length; index++) {
      if (source[index] === "\\") {
        index++;
        continue;
      }
      if (source[index] === "`") {
        let markerLength = 1;
        while (source[index + markerLength] === "`") markerLength++;
        const marker = "`".repeat(markerLength);
        const close = source.indexOf(marker, index + markerLength);
        if (close >= 0) {
          return { start: index, end: close + markerLength, tag: "code", content: source.slice(index + markerLength, close).trim() };
        }
      }
      for (const [marker, tag] of [["**", "strong"], ["__", "strong"], ["~~", "del"]]) {
        if (!source.startsWith(marker, index)) continue;
        const close = source.indexOf(marker, index + marker.length);
        if (close > index + marker.length) {
          return { start: index, end: close + marker.length, tag, content: source.slice(index + marker.length, close) };
        }
      }
      if (source[index] === "[") {
        const labelEnd = source.indexOf("](", index + 1);
        if (labelEnd > index + 1) {
          const targetEnd = findMarkdownLinkTargetEnd(source, labelEnd + 2);
          const href = targetEnd >= 0 ? safeMarkdownLink(source.slice(labelEnd + 2, targetEnd)) : "";
          if (href) {
            return { start: index, end: targetEnd + 1, tag: "a", content: source.slice(index + 1, labelEnd), href };
          }
        }
      }
      if (source[index] === "<") {
        const close = source.indexOf(">", index + 1);
        const content = close >= 0 ? source.slice(index + 1, close) : "";
        const href = safeMarkdownLink(content);
        if (href) return { start: index, end: close + 1, tag: "a", content, href };
      }
      if (source[index] === "*") {
        const close = source.indexOf("*", index + 1);
        if (close > index + 1) {
          return { start: index, end: close + 1, tag: "em", content: source.slice(index + 1, close) };
        }
      }
    }
    return null;
  }

  function findMarkdownLinkTargetEnd(source, start) {
    let depth = 0;
    for (let index = start; index < source.length; index++) {
      if (source[index] === "\\") {
        index++;
      } else if (source[index] === "(") {
        depth++;
      } else if (source[index] === ")") {
        if (depth === 0) return index;
        depth--;
      }
    }
    return -1;
  }

  function safeMarkdownLink(rawTarget) {
    const target = rawTarget.trim().replace(/^<|>$/g, "");
    if (!/^(https?:|mailto:)/i.test(target)) return "";
    try {
      return new URL(target).href;
    } catch (_) {
      return "";
    }
  }

  function appendMarkdownText(container, text) {
    container.append(document.createTextNode(text.replace(/\\([\\`*_[\]{}()#+\-.!>])/g, "$1")));
  }

  function projectNameForPath(path) {
    const project = (state.dashboard?.projects || []).find((item) => item.path === path);
    const cached = state.projectDetailCache.get(path)?.project;
    return cached?.name || project?.name || elements.detailTitle.textContent || "Project";
  }

  function hideSession() {
    closeSessionStream();
    closeQuickPanel();
    state.selectedSessionID = "";
    state.sessionDetailSignature = "";
    state.sessionTranscriptRevision = 0;
    state.sessionEntries = [];
    state.sessionEmptyMessage = "";
    state.sessionLastEntryKey = "";
    state.sessionHasNewActivity = false;
    state.sessionStickToBottom = true;
    state.sessionInput = null;
    state.sessionSubmitting = false;
    state.sessionFeedback = "";
    state.sessionLive = false;
    state.sessionRequestID++;
    elements.body.classList.remove("session-open");
    elements.sessionView.hidden = true;
    elements.sessionView.setAttribute("aria-hidden", "true");
    elements.sessionContent.hidden = true;
    elements.sessionActionDeck.hidden = true;
    elements.sessionProjectSummary.hidden = true;
    elements.sessionStandby.hidden = true;
    elements.sessionStandbyList.replaceChildren();
    elements.sessionState.replaceChildren();
    updateTranscriptControls();
  }

  function closeSession(updateHistory) {
    if (updateHistory && window.history.state?.sessionID === state.selectedSessionID) {
      window.history.back();
      return;
    }
    if (sessionFirstLayout()) {
      closeProject(updateHistory);
      return;
    }
    const projectPath = state.selectedPath;
    hideSession();
    if (!projectPath) {
      closeProject(updateHistory);
      return;
    }
    elements.body.classList.add("detail-open");
    elements.detailView.hidden = false;
    elements.detailView.removeAttribute("aria-hidden");
    document.title = `${projectNameForPath(projectPath)} - Little Control Room`;
    if (updateHistory) {
      window.history.pushState({ projectPath }, "", `#project=${encodeURIComponent(projectPath)}`);
    }
  }

  function closeProject(updateHistory) {
    hideSession();
    state.selectedPath = "";
    state.selectedProjectDetail = null;
    state.selectedProjectSessions = [];
    elements.body.classList.remove("detail-open");
    elements.detailView.hidden = true;
    elements.detailView.setAttribute("aria-hidden", "true");
    elements.detailContent.hidden = true;
    elements.detailState.replaceChildren();
    document.title = "Little Control Room";
    renderProjects();
    if (updateHistory && window.location.hash) {
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
    retry.addEventListener("click", () => openProject(state.selectedPath, false, true));
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
    elements.attentionCount.textContent = String(counts.attention || 0);
    elements.activeCount.textContent = String(counts.active || 0);
    elements.allCount.textContent = String(counts.all || 0);
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
    if (!state.authenticated) return;
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
        const sessionID = state.selectedSessionID;
        await loadDashboard(false);
        if (sessionID && state.selectedSessionID === sessionID) {
          if (!sessionStreamAvailable()) await loadSessionDetail(sessionID, false);
        } else if (state.selectedPath) {
          await openProject(state.selectedPath, false);
        }
      }, 350);
    });
    socket.addEventListener("close", () => {
      if (state.socket !== socket) return;
      setConnection("offline", "Reconnecting");
      state.reconnectTimer = window.setTimeout(async () => {
        try {
          const status = await readAuthStatus();
          if (status.required && !status.authenticated) {
            showAuthGate();
            return;
          }
          connectEvents();
        } catch (_error) {
          connectEvents();
        }
      }, 2500);
    });
    socket.addEventListener("error", () => socket.close());
  }

  async function openRouteFromLocation() {
    if (!state.authenticated) return;
    const hash = window.location.hash.startsWith("#") ? window.location.hash.slice(1) : "";
    const params = new URLSearchParams(hash);
    const projectPath = params.get("project") || "";
    const sessionID = params.get("session") || "";

    if (projectPath && sessionID) {
      if (projectPath !== state.selectedPath || !state.projectDetailCache.has(projectPath)) {
        await prepareProjectContext(projectPath);
      } else {
        state.selectedPath = projectPath;
        renderSessionProjectContext(state.projectDetailCache.get(projectPath));
      }
      if (sessionID !== state.selectedSessionID) await openSession(sessionID, false);
      return;
    }
    if (projectPath) {
      if (state.selectedSessionID) closeSession(false);
      if (projectPath !== state.selectedPath || elements.detailContent.hidden) await openProject(projectPath, false);
      return;
    }
    closeProject(false);
  }

  function formatClockTime(date) {
    return new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(date);
  }

  function updateSystemTime() {
    const now = new Date();
    elements.systemTime.dateTime = now.toISOString();
    elements.systemTime.textContent = formatClockTime(now);
  }

  async function submitPairingCode() {
    const code = elements.authCode.value.trim();
    if (!code) {
      elements.authError.textContent = "Enter the pairing code";
      return;
    }
    elements.authError.textContent = "";
    elements.authSubmit.disabled = true;
    const submitLabel = elements.authSubmit.querySelector("span:last-child");
    submitLabel.textContent = "Pairing";
    try {
      const response = await window.fetch("/api/mobile/auth/pair", {
        method: "POST",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/json",
        },
        cache: "no-store",
        body: JSON.stringify({ code }),
      });
      if (!response.ok) {
        if (response.status === 429) {
          const retryAfter = Number.parseInt(response.headers.get("Retry-After") || "60", 10);
          throw new Error(`Receiver locked. Try again in ${Number.isFinite(retryAfter) ? retryAfter : 60}s`);
        }
        if (response.status === 401) throw new Error("Pairing code not accepted");
        throw new Error((await response.text()).trim() || `Pairing failed (${response.status})`);
      }
      const status = await response.json();
      if (!status.authenticated) throw new Error("Pairing did not establish a session");
      releaseAuthGate(status);
      await startAuthenticatedApp();
    } catch (error) {
      elements.authError.textContent = error.message || "Could not pair this phone";
      elements.authCode.select();
    } finally {
      elements.authSubmit.disabled = false;
      submitLabel.textContent = "Pair this phone";
    }
  }

  elements.authForm.addEventListener("submit", (event) => {
    event.preventDefault();
    void submitPairingCode();
  });

  elements.authCode.addEventListener("input", () => {
    const digits = [...elements.authCode.value].filter((char) => char >= "0" && char <= "9").slice(0, 6);
    elements.authCode.value = digits.length > 3
      ? `${digits.slice(0, 3).join("")} ${digits.slice(3).join("")}`
      : digits.join("");
    elements.authError.textContent = "";
  });

  elements.authRetry.addEventListener("click", () => void bootstrap());

  elements.commandButton.addEventListener("click", async () => {
    if (!state.authenticated) {
      await bootstrap();
      return;
    }
    openQuickPanel("command");
  });

  elements.refreshButton.addEventListener("click", async () => {
    if (!state.authenticated) {
      await bootstrap();
      return;
    }
    const sessionID = state.selectedSessionID;
    await loadDashboard(true);
    if (sessionID && state.selectedSessionID === sessionID) {
      await loadSessionDetail(sessionID, false);
    } else if (state.selectedPath) {
      await openProject(state.selectedPath, false);
    }
  });

  elements.search.addEventListener("input", () => {
    state.query = elements.search.value;
    renderProjects();
  });

  elements.backButton.addEventListener("click", () => closeProject(true));
  elements.sessionBackButton.addEventListener("click", () => closeSession(true));
  elements.sessionActionDeck.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-quick-panel]");
    if (button) openQuickPanel(button.dataset.quickPanel);
  });
  elements.quickPanelClose.addEventListener("click", closeQuickPanel);
  elements.quickPanelDialog.addEventListener("click", (event) => {
    if (event.target === elements.quickPanelDialog) closeQuickPanel();
  });
  elements.quickPanelDialog.addEventListener("cancel", (event) => {
    event.preventDefault();
    closeQuickPanel();
  });
  elements.sessionInstruments.addEventListener("toggle", () => {
    elements.sessionInstrumentToggle.textContent = elements.sessionInstruments.open ? "-" : "+";
  });
  elements.transcriptMode.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-transcript-mode]");
    if (!button || button.dataset.transcriptMode === state.transcriptMode) return;
    state.transcriptMode = button.dataset.transcriptMode;
    window.localStorage.setItem("lcr.mobile.transcript-mode", state.transcriptMode);
    renderSessionTranscript();
    updateTranscriptControls();
    window.requestAnimationFrame(() => {
      if (state.sessionStickToBottom) scrollSessionToLatest();
    });
  });
  elements.sessionFollowButton.addEventListener("click", scrollSessionToLatest);
  elements.sessionComposer.addEventListener("submit", (event) => {
    event.preventDefault();
    void submitSessionMessage();
  });
  elements.sessionMessage.addEventListener("input", () => {
    state.sessionFeedback = "";
    persistSessionDraft();
    resizeSessionMessage();
    elements.sessionSendButton.disabled = state.sessionSubmitting
      || !state.sessionInput?.available
      || elements.sessionMessage.value.trim() === "";
    elements.sessionComposerFeedback.textContent = "";
  });
  elements.sessionMessage.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" || (!event.metaKey && !event.ctrlKey)) return;
    event.preventDefault();
    elements.sessionComposer.requestSubmit();
  });
  elements.sessionTranscript.addEventListener("scroll", () => {
    state.sessionStickToBottom = elements.sessionTranscript.scrollHeight
      - elements.sessionTranscript.scrollTop
      - elements.sessionTranscript.clientHeight < 72;
    if (state.sessionStickToBottom && state.sessionHasNewActivity) {
      state.sessionHasNewActivity = false;
      updateTranscriptControls();
    }
  }, { passive: true });
  window.addEventListener("popstate", openRouteFromLocation);
  window.addEventListener("resize", () => {
    if (!state.selectedSessionID || !state.sessionStickToBottom) return;
    window.requestAnimationFrame(() => {
      scrollSessionToLatest();
    });
  });
  window.addEventListener("keydown", (event) => {
    if (!state.authenticated) return;
    if (event.key !== "Escape") return;
    if (elements.quickPanelDialog.open) {
      closeQuickPanel();
      return;
    }
    if (state.selectedSessionID) {
      closeSession(true);
    } else if (state.selectedPath) {
      closeProject(true);
    }
  });

  updateSystemTime();
  window.setInterval(updateSystemTime, 30000);
  window.setInterval(() => {
    if (state.authenticated) refreshSelectedProjectSessions();
  }, 2500);
  void bootstrap();
})();
