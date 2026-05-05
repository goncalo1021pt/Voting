// Voting App — vanilla SPA, hash-routed, awards-editorial theme.
//
// Routes:
//   #/                            front page (logo + CTAs)
//   #/login                       login form
//   #/register                    register form
//   #/events                      Events tab — discover view
//   #/events/new                  create event (auth)
//   #/events/:id                  event detail
//   #/events/:id/results          all-categories results
//   #/events/:id/results/:catId   single-category results
//   #/invitations/:token          redeem invite (auth)
//   #/profile                     profile page (auth)

const TOKEN_KEY = "voting.token";
const USER_KEY = "voting.user";
const THEME_KEY = "voting.theme";

// ---------- Theme ----------

const theme = {
    get current() {
        return document.documentElement.getAttribute("data-theme") || "light";
    },
    apply(t) {
        document.documentElement.setAttribute("data-theme", t);
        try { localStorage.setItem(THEME_KEY, t); } catch {}
    },
    toggle() {
        this.apply(this.current === "dark" ? "light" : "dark");
    },
};

// ---------- Auth state ----------

const auth = {
    get token() { return localStorage.getItem(TOKEN_KEY); },
    get user() {
        const raw = localStorage.getItem(USER_KEY);
        return raw ? JSON.parse(raw) : null;
    },
    set(token, user) {
        localStorage.setItem(TOKEN_KEY, token);
        localStorage.setItem(USER_KEY, JSON.stringify(user));
    },
    clear() {
        localStorage.removeItem(TOKEN_KEY);
        localStorage.removeItem(USER_KEY);
    },
};

// ---------- API client ----------

async function api(method, path, body) {
    const headers = { "Content-Type": "application/json" };
    if (auth.token) headers["Authorization"] = `Bearer ${auth.token}`;
    const res = await fetch(path, {
        method,
        headers,
        body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    const text = await res.text();
    let data;
    try { data = text ? JSON.parse(text) : null; } catch { data = text; }
    if (!res.ok) {
        const msg = typeof data === "string" ? data : (data && data.error) || res.statusText;
        const err = new Error((msg || `HTTP ${res.status}`).trim());
        err.status = res.status;
        if (res.status === 401) {
            auth.clear();
            renderTopbar();
        }
        throw err;
    }
    return data;
}

const API = {
    me: () => api("GET", "/auth/me"),
    register: (body) => api("POST", "/auth/register", body),
    login: (body) => api("POST", "/auth/login", body),
    logout: () => api("POST", "/auth/logout"),
    listEvents: () => api("GET", "/events"),
    getEvent: (id) => api("GET", `/events/${id}`),
    createEvent: (body) => api("POST", "/events", body),
    closeEvent: (id) => api("POST", `/events/${id}/close`),
    joinEvent: (id) => api("POST", `/events/${id}/join`),
    createInvitation: (eventId) => api("POST", `/events/${eventId}/invitations`),
    redeemInvitation: (token) => api("POST", `/invitations/${token}`),
    vote: (categoryId, optionId) =>
        api("POST", "/votes", { category_id: categoryId, option_id: optionId }),
    getResults: (eventId, categoryId) =>
        api("GET", `/events/${eventId}/results/${categoryId}`),
    getAllResults: (eventId) => api("GET", `/events/${eventId}/results`),
    deleteEvent: (id) => api("DELETE", `/events/${id}`),
};

// ---------- Modal dialog ----------

// Returns a Promise<boolean> — resolves true if confirmed, false if cancelled.
function dialog({ eyebrow = "Confirm", title, body, confirm: confirmLabel = "Confirm", danger = false } = {}) {
    return new Promise((resolve) => {
        const backdrop = el("div", { class: "modal-backdrop" });

        function close(result) {
            backdrop.remove();
            resolve(result);
        }

        // Close on backdrop click (outside the modal card).
        backdrop.addEventListener("click", (e) => { if (e.target === backdrop) close(false); });

        // Close on Escape key.
        function onKey(e) { if (e.key === "Escape") { document.removeEventListener("keydown", onKey); close(false); } }
        document.addEventListener("keydown", onKey);

        const confirmBtn = el("button", {
            class: danger ? "danger" : "",
            onClick: () => { document.removeEventListener("keydown", onKey); close(true); },
        }, confirmLabel);

        const cancelBtn = el("button", {
            class: "secondary",
            onClick: () => { document.removeEventListener("keydown", onKey); close(false); },
        }, "Cancel");

        backdrop.appendChild(el("div", { class: "modal" }, [
            el("p", { class: "modal-eyebrow" }, eyebrow),
            el("h2", { class: "modal-title" }, title),
            body ? el("p", { class: "modal-body" }, body) : null,
            el("div", { class: "button-row" }, [confirmBtn, cancelBtn]),
        ]));

        document.body.appendChild(backdrop);
        // Focus the confirm button so Enter works immediately.
        confirmBtn.focus();
    });
}

// ---------- DOM helpers ----------

const $ = (sel) => document.querySelector(sel);

function el(tag, attrs = {}, children = []) {
    const node = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) {
        if (v === false || v === null || v === undefined) continue;
        if (k === "class") node.className = v;
        else if (k === "html") node.innerHTML = v;
        else if (k.startsWith("on") && typeof v === "function") {
            node.addEventListener(k.slice(2).toLowerCase(), v);
        } else if (k === "value") {
            node.value = v;
        } else if (v === true) {
            node.setAttribute(k, "");
        } else {
            node.setAttribute(k, v);
        }
    }
    for (const child of [].concat(children)) {
        if (child === null || child === undefined || child === false) continue;
        node.appendChild(typeof child === "string" ? document.createTextNode(child) : child);
    }
    return node;
}

function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); }

function render(...nodes) {
    const view = $("#view");
    clear(view);
    for (const n of nodes) {
        if (n === null || n === undefined || n === false) continue;
        view.appendChild(typeof n === "string" ? document.createTextNode(n) : n);
    }
}

let toastTimer = null;
function toast(msg, kind = "") {
    const t = $("#toast");
    t.textContent = msg;
    t.className = "toast" + (kind ? " " + kind : "");
    t.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { t.hidden = true; }, 3500);
}

// ---------- Inline SVG icons ----------

function svg(viewBox, paths, attrs = {}) {
    const ns = "http://www.w3.org/2000/svg";
    const node = document.createElementNS(ns, "svg");
    node.setAttribute("viewBox", viewBox);
    node.setAttribute("fill", "none");
    node.setAttribute("stroke", "currentColor");
    node.setAttribute("stroke-width", attrs["stroke-width"] || "1.8");
    node.setAttribute("stroke-linecap", "round");
    node.setAttribute("stroke-linejoin", "round");
    if (attrs["aria-hidden"]) node.setAttribute("aria-hidden", "true");
    for (const p of [].concat(paths)) {
        if (typeof p === "string") {
            const el = document.createElementNS(ns, "path");
            el.setAttribute("d", p);
            node.appendChild(el);
        } else {
            const el = document.createElementNS(ns, p.tag);
            for (const [k, v] of Object.entries(p.attrs || {})) el.setAttribute(k, v);
            node.appendChild(el);
        }
    }
    return node;
}

const icons = {
    sun: () => svg("0 0 24 24", [
        { tag: "circle", attrs: { cx: 12, cy: 12, r: 4 } },
        "M12 2v2", "M12 20v2", "M4.93 4.93l1.41 1.41", "M17.66 17.66l1.41 1.41",
        "M2 12h2", "M20 12h2", "M4.93 19.07l1.41-1.41", "M17.66 6.34l1.41-1.41",
    ], { "aria-hidden": true }),
    moon: () => svg("0 0 24 24", [
        "M21 12.79A9 9 0 1 1 11.21 3a7 7 0 0 0 9.79 9.79z",
    ], { "aria-hidden": true }),
    user: () => svg("0 0 24 24", [
        { tag: "circle", attrs: { cx: 12, cy: 8, r: 4 } },
        "M4 21a8 8 0 0 1 16 0",
    ], { "aria-hidden": true }),
    flourish: () => svg("0 0 24 24", [
        { tag: "circle", attrs: { cx: 12, cy: 12, r: 2 } },
    ], { "aria-hidden": true, "stroke-width": 1.5 }),
};

// ---------- Logo (large) ----------

function bigLogo() {
    return svg("0 0 100 100", [
        { tag: "circle", attrs: { cx: 50, cy: 50, r: 44, "stroke-width": 2 } },
        { tag: "g", attrs: {} },
        // tick marks at 12 / 3 / 6 / 9
        { tag: "line", attrs: { x1: 50, y1: 4, x2: 50, y2: 13, "stroke-width": 2 } },
        { tag: "line", attrs: { x1: 50, y1: 87, x2: 50, y2: 96, "stroke-width": 2 } },
        { tag: "line", attrs: { x1: 4, y1: 50, x2: 13, y2: 50, "stroke-width": 2 } },
        { tag: "line", attrs: { x1: 87, y1: 50, x2: 96, y2: 50, "stroke-width": 2 } },
        // small ticks at 1.5 / 4.5 / 7.5 / 10.5
        { tag: "line", attrs: { x1: 18, y1: 18, x2: 24, y2: 24, "stroke-width": 1.5 } },
        { tag: "line", attrs: { x1: 76, y1: 18, x2: 82, y2: 24, "stroke-width": 1.5 } },
        { tag: "line", attrs: { x1: 18, y1: 82, x2: 24, y2: 76, "stroke-width": 1.5 } },
        { tag: "line", attrs: { x1: 76, y1: 82, x2: 82, y2: 76, "stroke-width": 1.5 } },
        // V mark
        "M28 32 L50 72 L72 32",
    ], { "aria-hidden": true, "stroke-width": 2 });
}

// ---------- Topbar ----------

function renderTopbar() {
    const nav = $("#nav");
    const actions = $("#actions");
    clear(nav);
    clear(actions);

    if (auth.token) {
        nav.appendChild(navLink("#/events", "Events"));
    }

    // Theme toggle (always visible)
    const toggle = el("button", {
        class: "icon-button theme-toggle",
        "aria-label": "Toggle theme",
        title: "Toggle theme",
        onClick: () => theme.toggle(),
    });
    const sun = icons.sun();
    sun.classList.add("sun");
    const moon = icons.moon();
    moon.classList.add("moon");
    toggle.appendChild(sun);
    toggle.appendChild(moon);
    actions.appendChild(toggle);

    if (auth.token) {
        // Profile button
        const initials = (auth.user?.username || "?")[0].toUpperCase();
        const profileBtn = el("a", {
            class: "icon-button profile-button",
            href: "#/profile",
            "aria-label": "Open profile",
            title: auth.user?.username || "Profile",
        }, initials);
        actions.appendChild(profileBtn);
    } else {
        actions.appendChild(el("a", { class: "btn secondary", href: "#/login" }, "Log in"));
        actions.appendChild(el("a", { class: "btn", href: "#/register" }, "Register"));
    }

    setActiveNav();
}

function navLink(href, label) {
    const a = el("a", { href }, label);
    a.dataset.route = href;
    return a;
}

function setActiveNav() {
    const hash = location.hash || "#/";
    document.querySelectorAll("#nav a").forEach((a) => {
        const route = a.dataset.route || "";
        const active = route !== "#/" && hash.startsWith(route);
        a.classList.toggle("active", active);
    });
}

// ---------- Router ----------

const routes = [
    { pattern: /^#?\/?$/, view: viewHome },
    { pattern: /^#?\/login$/, view: viewLogin },
    { pattern: /^#?\/register$/, view: viewRegister },
    { pattern: /^#?\/events$/, view: viewEvents, requireAuth: true },
    { pattern: /^#?\/events\/new$/, view: viewCreateEvent, requireAuth: true },
    { pattern: /^#?\/events\/(\d+)\/results\/(\d+)$/, view: viewResults, params: ["eventId", "categoryId"] },
    { pattern: /^#?\/events\/(\d+)\/results$/, view: viewAllResults, params: ["eventId"] },
    { pattern: /^#?\/events\/(\d+)$/, view: viewEvent, params: ["eventId"] },
    { pattern: /^#?\/invitations\/([^/]+)$/, view: viewRedeemInvitation, params: ["token"], requireAuth: true },
    { pattern: /^#?\/profile$/, view: viewProfile, requireAuth: true },
];

function navigate(hash) {
    if (location.hash !== hash) location.hash = hash; else router();
}

async function router() {
    const hash = location.hash || "#/";
    setActiveNav();
    for (const r of routes) {
        const m = hash.match(r.pattern);
        if (!m) continue;
        const params = {};
        if (r.params) r.params.forEach((p, i) => params[p] = m[i + 1]);
        if (r.requireAuth && !auth.token) {
            toast("Please log in first", "error");
            navigate("#/login");
            return;
        }
        try {
            await r.view(params);
        } catch (err) {
            if (err.status === 401) {
                toast("Your session has expired. Please log in again.", "error");
                navigate("#/login");
                return;
            }
            console.error(err);
            render(el("div", { class: "error-box" }, err.message || String(err)));
        }
        window.scrollTo({ top: 0, behavior: "instant" });
        return;
    }
    render(el("div", { class: "empty" }, "Not found."));
}

// ---------- Views ----------

async function viewHome() {
    document.querySelector("main").classList.remove("narrow", "wide");

    const ctas = auth.token
        ? [
            el("a", { class: "btn", href: "#/events" }, "Browse events"),
            el("a", { class: "btn secondary", href: "#/events/new" }, "Host an event"),
        ]
        : [
            el("a", { class: "btn", href: "#/register" }, "Create account"),
            el("a", { class: "btn secondary", href: "#/login" }, "Log in"),
        ];

    const ornament = el("div", { class: "ornament" }, [icons.flourish()]);

    const hero = el("section", { class: "hero" }, [
        el("div", { class: "logo-mark" }, [bigLogo()]),
        el("div", {}, [
            el("p", { class: "eyebrow" }, "An awards-show in a browser tab"),
            el("h1", { class: "wordmark" }, [
                "Voting ",
                el("em", {}, "App"),
            ]),
        ]),
        el("p", { class: "tagline" },
            "Host an evening of categories, nominees, and ceremony — or join the audience and cast your votes."),
        ornament,
        el("div", { class: "hero-actions" }, ctas),
    ]);

    render(hero);
}

async function viewLogin() {
    document.querySelector("main").classList.add("narrow");
    const form = el("form", {
        onSubmit: async (e) => {
            e.preventDefault();
            const username = form.username.value.trim();
            const password = form.password.value;
            if (!username || !password) { toast("Username and password required", "error"); return; }
            try {
                const res = await API.login({ username, password });
                auth.set(res.token, res.user);
                renderTopbar();
                toast(`Welcome back, ${res.user.username}`, "success");
                navigate("#/events");
            } catch (err) {
                toast(err.message || "Login failed", "error");
            }
        },
    }, [
        el("label", { for: "login-username" }, ["Username", el("input", { id: "login-username", type: "text", name: "username", autocomplete: "username", required: true })]),
        el("label", { for: "login-password" }, ["Password", el("input", { id: "login-password", type: "password", name: "password", autocomplete: "current-password", required: true })]),
        el("div", { class: "button-row" }, [
            el("button", { type: "submit" }, "Log in"),
            el("a", { class: "btn secondary", href: "#/register" }, "Register instead"),
        ]),
    ]);
    render(
        el("p", { class: "eyebrow" }, "Sign in"),
        el("h1", {}, "Welcome back."),
        form,
    );
}

async function viewRegister() {
    document.querySelector("main").classList.add("narrow");
    const form = el("form", {
        onSubmit: async (e) => {
            e.preventDefault();
            const username = form.username.value.trim();
            const email = form.email.value.trim();
            const password = form.password.value;
            if (!username || !email || !password) { toast("All fields required", "error"); return; }
            if (password.length < 6) { toast("Password must be at least 6 characters", "error"); return; }
            try {
                const res = await API.register({ username, email, password });
                auth.set(res.token, res.user);
                renderTopbar();
                toast(`Account created — welcome, ${res.user.username}`, "success");
                navigate("#/events");
            } catch (err) {
                toast(err.message || "Registration failed", "error");
            }
        },
    }, [
        el("label", { for: "reg-username" }, ["Username", el("input", { id: "reg-username", type: "text", name: "username", autocomplete: "off", required: true })]),
        el("label", { for: "reg-email" }, ["Email", el("input", { id: "reg-email", type: "email", name: "email", autocomplete: "email", required: true })]),
        el("label", { for: "reg-password" }, ["Password (min 6 chars)", el("input", { id: "reg-password", type: "password", name: "password", autocomplete: "off", required: true })]),
        el("div", { class: "button-row" }, [
            el("button", { type: "submit" }, "Create account"),
            el("a", { class: "btn secondary", href: "#/login" }, "Log in instead"),
        ]),
    ]);
    render(
        el("p", { class: "eyebrow" }, "Begin"),
        el("h1", {}, "Create an account."),
        form,
    );
}

// ---------- Events list ----------

async function viewEvents() {
    document.querySelector("main").classList.remove("narrow");
    let events;
    try {
        events = await API.listEvents();
    } catch (err) {
        throw err;
    }
    events = events || [];

    // is_member is now returned by the API for authenticated users.
    const yours = events.filter((e) => e.is_member);
    const joinable = events.filter((e) => !e.is_member && e.visibility === "public" && e.is_active);

    const header = el("div", { class: "card-row" }, [
        el("div", {}, [
            el("p", { class: "eyebrow" }, "Programme"),
            el("h1", {}, "Events"),
            el("p", { class: "subtitle" }, "Find an open ceremony to attend, or revisit the ones you’re part of."),
        ]),
        el("a", { class: "btn", href: "#/events/new" }, "Host an event"),
    ]);

    const sectionJoinable = section("Available to join", joinable.length
        ? joinable.map((e) => eventCard(e, {
            onJoin: async () => {
                try {
                    await API.joinEvent(e.id);
                    toast("Joined event", "success");
                    router();
                } catch (err) { toast(err.message || "Failed to join", "error"); }
            },
        }))
        : [emptyNote("No open public events right now.")]);

    const sectionYours = section("Your events", yours.length
        ? yours.map((e) => eventCard(e, { mine: true }))
        : [emptyNote("You haven’t hosted or joined anything yet.")]);

    render(header, sectionJoinable, sectionYours);
}

function section(title, children) {
    if (!children) return null;
    return el("section", {}, [
        el("h2", {}, title),
        ...[].concat(children).filter(Boolean),
    ]);
}

function emptyNote(text) {
    return el("div", { class: "empty" }, text);
}

function eventCard(e, opts = {}) {
    const tags = [
        el("span", { class: "tag " + (e.visibility === "public" ? "subtle" : "accent") }, e.visibility),
        el("span", { class: "tag subtle" }, e.results_visibility === "live" ? "live results" : "results after close"),
        el("span", { class: e.is_active ? "tag success" : "tag danger" }, e.is_active ? "open" : "closed"),
        opts.mine && auth.user && e.host_id === auth.user.id ? el("span", { class: "tag accent" }, "you host") : null,
    ];

    const body = [
        el("h3", {}, e.name),
        e.description ? el("p", { class: "muted" }, e.description) : null,
        el("div", { class: "card-meta" }, tags),
        opts.onJoin ? el("div", { class: "button-row", style: "margin-top:14px;" }, [
            el("button", { class: "secondary", onClick: opts.onJoin }, "Join event"),
        ]) : null,
    ];

    // Cards with a join action are not navigable — clicking the card does nothing.
    if (opts.onJoin) {
        return el("div", { class: "card" }, body);
    }
    return el("a", { class: "card", href: `#/events/${e.id}` }, body);
}

// ---------- Create event ----------

async function viewCreateEvent() {
    document.querySelector("main").classList.add("wide");

    let categoryCount = 1;
    const categoriesContainer = el("div", { id: "categories" });

    function addCategory() {
        const idx = categoryCount++;
        const optionsContainer = el("div", { class: "options" });
        const block = el("div", { class: "category-block" }, [
            el("div", { class: "card-row" }, [
                el("strong", {}, `Category ${idx}`),
                el("button", {
                    type: "button",
                    class: "link",
                    onClick: () => block.remove(),
                }, "Remove"),
            ]),
            el("label", {}, ["Name", el("input", { name: `cat-${idx}-name`, required: true, placeholder: "e.g. Game of the Year" })]),
            el("p", { class: "eyebrow", style: "margin-top:8px;" }, "Options"),
            optionsContainer,
            el("button", {
                type: "button",
                class: "secondary",
                onClick: () => optionsContainer.appendChild(makeOptionRow()),
            }, "+ Add option"),
        ]);
        optionsContainer.appendChild(makeOptionRow());
        optionsContainer.appendChild(makeOptionRow());
        categoriesContainer.appendChild(block);
    }

    function makeOptionRow() {
        const row = el("div", { class: "option-row" });
        const input = el("input", { class: "option-name", placeholder: "Option name", required: true });
        const remove = el("button", { type: "button", class: "secondary", onClick: () => row.remove() }, "×");
        row.appendChild(input);
        row.appendChild(remove);
        return row;
    }

    const form = el("form", {
        class: "wide",
        onSubmit: async (e) => {
            e.preventDefault();
            const name = form.elements["name"].value.trim();
            const description = form.elements["description"].value.trim();
            const visibility = form.elements["visibility"].value;
            const resultsVisibility = form.elements["results_visibility"].value;
            const requireFullBallot = form.elements["require_full_ballot"].checked;
            const categories = [];
            for (const block of categoriesContainer.querySelectorAll(".category-block")) {
                const catName = block.querySelector("input[name^='cat-']").value.trim();
                if (!catName) continue;
                const options = Array.from(block.querySelectorAll(".option-name"))
                    .map((i) => i.value.trim())
                    .filter(Boolean);
                if (options.length < 2) {
                    toast(`Category "${catName}" needs at least 2 options`, "error");
                    return;
                }
                categories.push({ name: catName, options });
            }
            if (!name) { toast("Event name required", "error"); return; }
            if (categories.length === 0) { toast("Add at least one category", "error"); return; }
            try {
                const created = await API.createEvent({
                    name, description, visibility,
                    results_visibility: resultsVisibility,
                    require_full_ballot: requireFullBallot,
                    categories,
                });
                toast("Event created", "success");
                navigate(`#/events/${created.id}`);
            } catch (err) {
                toast(err.message || "Failed to create event", "error");
            }
        },
    }, [
        el("label", {}, ["Event name", el("input", { name: "name", required: true, placeholder: "e.g. Game Awards 2026" })]),
        el("label", {}, ["Description", el("textarea", { name: "description", placeholder: "Optional" })]),
        el("label", {}, [
            "Visibility",
            el("select", { name: "visibility" }, [
                el("option", { value: "invite-only" }, "Invite-only"),
                el("option", { value: "public" }, "Public"),
            ]),
        ]),
        el("label", {}, [
            "Results visibility",
            el("select", { name: "results_visibility" }, [
                el("option", { value: "after_conclusion" }, "After event closes (default)"),
                el("option", { value: "live" }, "Live"),
            ]),
        ]),
        el("label", { class: "checkbox-label" }, [
            el("input", { type: "checkbox", name: "require_full_ballot" }),
            "Require voters to fill all categories before submitting",
        ]),
        el("h2", {}, "Categories"),
        categoriesContainer,
        el("div", { class: "button-row" }, [
            el("button", { type: "button", class: "secondary", onClick: addCategory }, "+ Add category"),
        ]),
        el("div", { class: "button-row" }, [
            el("button", { type: "submit" }, "Create event"),
            el("a", { class: "btn secondary", href: "#/events" }, "Cancel"),
        ]),
    ]);

    addCategory();

    render(
        el("p", { class: "eyebrow" }, "New ceremony"),
        el("h1", {}, "Host an event."),
        form,
    );
}

// ---------- Event detail ----------

async function viewEvent({ eventId }) {
    document.querySelector("main").classList.remove("narrow");
    let event;
    try {
        event = await API.getEvent(eventId);
    } catch (err) {
        throw err;
    }

    const isHost = auth.user && event.host_id === auth.user.id;
    const myVotes = event.my_votes || {};
    const categories = event.categories || [];
    const canVote = !!(auth.token && event.is_active && event.is_member);
    const unvotedCategories = categories.filter((c) => myVotes[c.id] == null);
    const showResultsLink = !event.is_active || event.results_visibility === "live" || isHost;

    const memberCount = event.member_count || 0;
    const tags = [
        el("span", { class: "tag " + (event.visibility === "public" ? "subtle" : "accent") }, event.visibility),
        el("span", { class: "tag subtle" }, event.results_visibility === "live" ? "live results" : "results after close"),
        el("span", { class: event.is_active ? "tag success" : "tag danger" }, event.is_active ? "open" : "closed"),
        el("span", { class: "tag subtle" }, `${memberCount} member${memberCount === 1 ? "" : "s"}`),
        isHost ? el("span", { class: "tag accent" }, "you host") : null,
        event.require_full_ballot ? el("span", { class: "tag subtle" }, "full ballot required") : null,
    ];

    const headerCard = el("section", {}, [
        el("p", { class: "eyebrow" }, "Event"),
        el("h1", {}, event.name),
        event.description ? el("p", { class: "subtitle" }, event.description) : null,
        el("div", { class: "card-meta" }, tags),
        el("div", { class: "button-row", style: "margin-top:18px;" }, [
            auth.token && event.visibility === "public" && event.is_active && !event.is_member
                ? el("button", {
                    class: "secondary",
                    onClick: async () => {
                        try { await API.joinEvent(event.id); toast("Joined event", "success"); router(); }
                        catch (err) { toast(err.message || "Failed to join", "error"); }
                    },
                }, "Join event") : null,
            isHost && event.is_active ? el("button", {
                class: "secondary",
                onClick: async () => {
                    try {
                        const inv = await API.createInvitation(event.id);
                        const link = `${location.origin}/#/invitations/${inv.token}`;
                        try { await navigator.clipboard.writeText(link); } catch {}
                        toast(`Invite link copied: ${link}`, "success");
                    } catch (err) { toast(err.message || "Failed to create invite", "error"); }
                },
            }, "Create invite link") : null,
            isHost && event.is_active ? el("button", {
                class: "danger",
                onClick: async () => {
                    const ok = await dialog({
                        eyebrow: "Host action",
                        title: "Close this event?",
                        body: "Voting will stop and results will become visible to all members.",
                        confirm: "Close event",
                        danger: true,
                    });
                    if (!ok) return;
                    try { await API.closeEvent(event.id); toast("Event closed", "success"); router(); }
                    catch (err) { toast(err.message || "Failed to close", "error"); }
                },
            }, "Close event") : null,
            isHost ? el("button", {
                class: "danger",
                onClick: async () => {
                    const ok = await dialog({
                        eyebrow: "Danger zone",
                        title: "Delete this event?",
                        body: "All categories, options, and votes will be permanently removed. This cannot be undone.",
                        confirm: "Delete event",
                        danger: true,
                    });
                    if (!ok) return;
                    try { await API.deleteEvent(event.id); toast("Event deleted", "success"); navigate("#/events"); }
                    catch (err) { toast(err.message || "Failed to delete", "error"); }
                },
            }, "Delete event") : null,
        ]),
    ]);

    if (categories.length === 0) {
        render(headerCard, el("p", { class: "muted" }, "No categories."));
        return;
    }

    // Non-member trying to view an invite-only event — show a wall instead of categories.
    if (!event.is_member && event.visibility === "invite-only") {
        render(
            headerCard,
            el("div", { class: "card" }, [
                el("p", { class: "eyebrow" }, "Access restricted"),
                el("p", {}, "This event is invite-only. You need an invitation link from the host to join."),
                el("div", { class: "button-row", style: "margin-top:14px;" }, [
                    el("a", { class: "btn secondary", href: "#/events" }, "← All events"),
                ]),
            ]),
        );
        return;
    }

    // Ballot draft state — shared across all category blocks.
    const drafts = {};
    let submitBtn = null;

    function updateSubmitState() {
        if (!submitBtn) return;
        const hasAny = Object.keys(drafts).length > 0;
        const allFilled = unvotedCategories.every((c) => drafts[c.id] != null);
        submitBtn.disabled = !hasAny || (event.require_full_ballot && !allFilled);
    }

    const categoryBlocks = categories.map((cat) => {
        const votedOptionId = myVotes[cat.id];
        const isVoted = votedOptionId != null;

        const optionsList = el("div", { class: "vote-options" });
        (cat.options || []).forEach((opt) => {
            const isMyVote = isVoted && String(votedOptionId) === String(opt.id);
            const row = el("div", {
                class: "vote-option" + (isMyVote ? " selected" : "") + (!isVoted && canVote ? " selectable" : ""),
            }, [
                el("span", { class: "option-name" }, opt.name),
                isVoted && isMyVote ? el("span", { class: "tag success" }, "Your vote") : null,
            ]);

            if (!isVoted && canVote) {
                row.addEventListener("click", () => {
                    if (drafts[cat.id] === opt.id) {
                        delete drafts[cat.id];
                    } else {
                        drafts[cat.id] = opt.id;
                    }
                    Array.from(optionsList.children).forEach((r) => r.classList.remove("selected"));
                    if (drafts[cat.id] != null) row.classList.add("selected");
                    updateSubmitState();
                });
            }

            optionsList.appendChild(row);
        });

        return el("article", { class: "card" }, [
            el("h3", {}, cat.name),
            optionsList,
            showResultsLink ? null : el("p", { class: "muted", style: "margin-top:8px;" }, "Results after event closes."),
        ]);
    });

    // Ballot submit row — only shown if the user can still vote in some categories.
    let ballotRow = null;
    if (canVote && unvotedCategories.length > 0) {
        submitBtn = el("button", { type: "button", disabled: true }, "Submit votes");
        updateSubmitState();

        submitBtn.addEventListener("click", async () => {
            submitBtn.disabled = true;
            submitBtn.textContent = "Submitting…";
            let errors = 0;
            for (const [catId, optId] of Object.entries(drafts)) {
                try {
                    await API.vote(Number(catId), Number(optId));
                } catch (err) {
                    if (err.status !== 409) errors++;
                }
            }
            if (errors > 0) toast(`${errors} vote(s) failed`, "error");
            else toast("Votes submitted", "success");
            router();
        });

        const hint = el("p", { class: "muted" }, event.require_full_ballot
            ? "All categories must be filled to submit."
            : "You can submit a partial ballot.");

        ballotRow = el("div", { class: "ballot-row" }, [submitBtn, hint]);
    }

    const resultsLink = showResultsLink
        ? el("div", { class: "button-row", style: "margin-bottom:14px;" }, [
            el("a", { class: "btn secondary", href: `#/events/${event.id}/results` }, "View results"),
        ])
        : null;

    render(
        headerCard,
        el("section", {}, [el("h2", {}, "Categories"), resultsLink, ...categoryBlocks, ballotRow]),
    );
}

// ---------- Results ----------

// Build the sorted bar list for one category's tally.
function resultsBars(results) {
    const total = (results || []).reduce((s, r) => s + (r.votes || 0), 0);
    const sorted = (results || []).slice().sort((a, b) => b.votes - a.votes);
    return sorted.map((r, i) => {
        const pct = total > 0 ? Math.round((r.votes / total) * 100) : 0;
        const isWinner = i === 0 && total > 0;
        return el("div", { class: "results-bar" }, [
            el("div", { class: "card-row" }, [
                el("div", { style: "display:flex;align-items:center;gap:8px;" }, [
                    isWinner ? el("span", { class: "tag accent" }, "Winner") : null,
                    el("span", { class: "option-name" }, r.option_name),
                ]),
                el("span", { class: "muted" }, `${r.votes} vote${r.votes === 1 ? "" : "s"} (${pct}%)`),
            ]),
            el("div", { class: "results-bar-track" }, [
                el("div", { class: "results-bar-fill" + (isWinner ? " winner" : ""), style: `width:${pct}%` }),
            ]),
        ]);
    });
}

async function viewResults({ eventId, categoryId }) {
    document.querySelector("main").classList.remove("narrow");
    const results = await API.getResults(eventId, categoryId);
    const bars = resultsBars(results.results);
    const total = results.total_votes || 0;
    const memberCount = results.member_count || 0;
    const participation = memberCount > 0
        ? `${total} of ${memberCount} member${memberCount === 1 ? "" : "s"} voted`
        : `${total} total vote${total === 1 ? "" : "s"}`;

    render(
        el("p", { class: "eyebrow" }, "Results"),
        el("h1", {}, results.category_name),
        el("p", { class: "muted" }, participation),
        el("section", { class: "card" }, bars.length ? bars : [el("p", { class: "muted" }, "No votes yet.")]),
        el("div", { class: "button-row" }, [
            el("a", { class: "btn secondary", href: `#/events/${eventId}/results` }, "All categories"),
            el("a", { class: "btn secondary", href: `#/events/${eventId}` }, "← Back to event"),
        ]),
    );
}

async function viewAllResults({ eventId }) {
    document.querySelector("main").classList.remove("narrow");
    const data = await API.getAllResults(eventId);
    const memberCount = data.member_count || 0;
    const cats = data.categories || [];

    const cards = cats.map((cat) => {
        const bars = resultsBars(cat.results);
        const total = cat.total_votes || 0;
        return el("section", { class: "card" }, [
            el("div", { class: "card-row" }, [
                el("h3", {}, cat.category_name),
                el("span", { class: "muted" }, `${total} vote${total === 1 ? "" : "s"}`),
            ]),
            bars.length ? el("div", {}, bars) : el("p", { class: "muted" }, "No votes yet."),
        ]);
    });

    const summary = memberCount > 0
        ? `${memberCount} member${memberCount === 1 ? "" : "s"} • ${data.is_active ? "voting open" : "closed"}`
        : (data.is_active ? "voting open" : "closed");

    render(
        el("p", { class: "eyebrow" }, "Results"),
        el("h1", {}, data.event_name),
        el("p", { class: "muted" }, summary),
        cards.length ? el("section", {}, cards) : el("p", { class: "muted" }, "No categories."),
        el("div", { class: "button-row" }, [
            el("a", { class: "btn secondary", href: `#/events/${eventId}` }, "← Back to event"),
            el("a", { class: "btn secondary", href: "#/events" }, "← All events"),
        ]),
    );
}

async function viewRedeemInvitation({ token }) {
    document.querySelector("main").classList.add("narrow");
    render(el("p", { class: "muted" }, "Redeeming invitation…"));
    try {
        const res = await API.redeemInvitation(token);
        toast("Joined event via invitation", "success");
        navigate(`#/events/${res.event_id}`);
    } catch (err) {
        render(
            el("p", { class: "eyebrow" }, "Invitation"),
            el("h1", {}, "Hmm."),
            el("div", { class: "error-box" }, err.message || "Could not redeem invitation."),
            el("div", { class: "button-row" }, [
                el("a", { class: "btn secondary", href: "#/events" }, "Back to events"),
            ]),
        );
    }
}

// ---------- Profile ----------

async function viewProfile() {
    document.querySelector("main").classList.remove("narrow");
    const user = auth.user;
    const initials = (user?.username || "?")[0].toUpperCase();

    let events = [];
    try {
        events = await API.listEvents();
    } catch {
        // non-critical; profile renders with empty sections
    }
    events = events || [];

    const myID = user?.id;
    const hosting = events.filter((e) => e.host_id === myID);
    const joined = events.filter((e) => e.is_member && e.host_id !== myID);

    const profileHero = el("section", { class: "profile-hero" }, [
        el("div", { class: "avatar" }, initials),
        el("div", {}, [
            el("p", { class: "eyebrow" }, "Profile"),
            el("h1", {}, user?.username || ""),
            el("p", { class: "muted" }, user?.email || ""),
        ]),
        el("div", { style: "margin-left:auto;" }, [
            el("button", {
                class: "secondary",
                onClick: async () => {
                    try { await API.logout(); } catch {}
                    auth.clear();
                    renderTopbar();
                    toast("Logged out", "success");
                    navigate("#/");
                },
            }, "Log out"),
        ]),
    ]);

    const hostingSection = section("Events you host",
        hosting.length
            ? hosting.map((e) => eventCard(e, { mine: true }))
            : [emptyNote("You haven’t hosted an event yet."),
               el("div", { class: "button-row", style: "justify-content:center;" }, [
                   el("a", { class: "btn", href: "#/events/new" }, "Host one"),
               ])]);

    const joinedSection = section("Events you’ve joined",
        joined.length
            ? joined.map((e) => eventCard(e, { mine: true }))
            : [emptyNote("You haven’t joined an invite-only event yet.")]);

    render(profileHero, hostingSection, joinedSection);
}

// ---------- Boot ----------

window.addEventListener("hashchange", router);
window.addEventListener("DOMContentLoaded", async () => {
    // Validate stored token before rendering anything — clears stale sessions.
    if (auth.token) {
        try {
            const user = await API.me();
            // Sync stored user data in case it changed server-side.
            auth.set(auth.token, user);
        } catch {
            // api() already cleared auth on 401; other errors leave state as-is.
        }
    }
    renderTopbar();
    router();
});
