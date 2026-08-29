// Events — vanilla SPA, hash-routed, awards-editorial theme.
//
// Routes:
//   #/                            front page (logo + CTAs)
//   #/login                       login form
//   #/register                    register form
//   #/events                      Events tab — discover view
//   #/events/new                  create event (auth)
//   #/events/:id                  event detail
//   #/events/:id/edit             edit event (host)
//   #/events/:id/results          all-categories results
//   #/events/:id/results/:catId   single-category results
//   #/invitations/:token          redeem invite (auth)
//   #/profile                     profile page (auth)

const TOKEN_KEY = "events.token";
const USER_KEY = "events.user";
const THEME_KEY = "events.theme";

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

// Which sign-in methods this deployment offers, filled in at boot from
// /auth/config. Defaults to password-only, so a failed or slow config request
// hides the Google button rather than offering one the server would refuse.
let signInMethods = { google: false };

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
    updateProfile: (body) => api("PATCH", "/auth/me", body),
    authConfig: () => api("GET", "/auth/config"),
    googleExchange: (code) => api("POST", "/auth/google/exchange", { code }),
    listEvents: () => api("GET", "/events"),
    getEvent: (id) => api("GET", `/events/${id}`),
    createEvent: (body) => api("POST", "/events", body),
    updateEvent: (id, body) => api("PUT", `/events/${id}`, body),
    closeEvent: (id) => api("POST", `/events/${id}/close`),
    joinEvent: (id) => api("POST", `/events/${id}/join`),
    createInvitation: (eventId, body) => api("POST", `/events/${eventId}/invitations`, body),
    listInvitations: (eventId) => api("GET", `/events/${eventId}/invitations`),
    revokeInvitation: (eventId, token) => api("DELETE", `/events/${eventId}/invitations/${token}`),
    redeemInvitation: (token) => api("POST", `/invitations/${token}`),
    listMembers: (eventId) => api("GET", `/events/${eventId}/members`),
    removeMember: (eventId, userId) => api("DELETE", `/events/${eventId}/members/${userId}`),
    vote: (categoryId, optionId) =>
        api("POST", "/votes", { category_id: categoryId, option_id: optionId }),
    // One request for the whole ballot: the server records it in a single
    // transaction, so a submission either lands completely or not at all.
    submitBallot: (eventId, votes) =>
        api("POST", `/events/${eventId}/ballot`, { votes }),
    getResults: (eventId, categoryId) =>
        api("GET", `/events/${eventId}/results/${categoryId}`),
    getAllResults: (eventId) => api("GET", `/events/${eventId}/results`),
    deleteEvent: (id) => api("DELETE", `/events/${id}`),
};

// ---------- Event import (JSON) ----------
//
// Schema:
//   {
//     "name": "...",                    required
//     "description": "...",             optional
//     "visibility": "public" | "invite-only",   default "invite-only"
//     "results_visibility": "after_conclusion" | "live",  default "after_conclusion"
//     "require_full_ballot": true|false,  default false
//     "lists": { "name": ["a", "b", ...] },   optional, reusable option lists
//     "categories": [
//       { "name": "...",                  required
//         "description": "...",           optional
//         "options": "listName" | ["opt1", "opt2", ...] }
//     ]
//   }
//
// Returns the payload accepted by POST /events. Throws on invalid input.
function parseEventImport(text) {
    let doc;
    try {
        doc = JSON.parse(text);
    } catch (err) {
        throw new Error(`Invalid JSON: ${err.message}`);
    }
    if (!doc || typeof doc !== "object" || Array.isArray(doc)) {
        throw new Error("Top-level value must be an object");
    }
    const name = typeof doc.name === "string" ? doc.name.trim() : "";
    if (!name) throw new Error('Missing required field "name"');

    const visibility = doc.visibility === "public" ? "public" : "invite-only";
    const resultsVisibility = doc.results_visibility === "live" ? "live" : "after_conclusion";
    const requireFullBallot = doc.require_full_ballot === true;

    const lists = doc.lists && typeof doc.lists === "object" ? doc.lists : {};
    for (const [listName, items] of Object.entries(lists)) {
        if (!Array.isArray(items) || !items.every((s) => typeof s === "string")) {
            throw new Error(`List "${listName}" must be an array of strings`);
        }
    }

    if (!Array.isArray(doc.categories) || doc.categories.length === 0) {
        throw new Error('"categories" must be a non-empty array');
    }
    const categories = doc.categories.map((cat, i) => {
        if (!cat || typeof cat !== "object") {
            throw new Error(`Category #${i + 1} must be an object`);
        }
        const catName = typeof cat.name === "string" ? cat.name.trim() : "";
        if (!catName) throw new Error(`Category #${i + 1} is missing "name"`);

        let options;
        if (typeof cat.options === "string") {
            const ref = lists[cat.options];
            if (!ref) throw new Error(`Category "${catName}" references unknown list "${cat.options}"`);
            options = ref.slice();
        } else if (Array.isArray(cat.options) && cat.options.every((s) => typeof s === "string")) {
            options = cat.options.slice();
        } else {
            throw new Error(`Category "${catName}" needs "options" as a list name (string) or an array of strings`);
        }
        options = options.map((s) => s.trim()).filter(Boolean);
        if (options.length < 2) {
            throw new Error(`Category "${catName}" needs at least 2 options`);
        }
        const catDescription = typeof cat.description === "string" ? cat.description.trim() : "";
        return { name: catName, description: catDescription, options };
    });

    return {
        name,
        description: typeof doc.description === "string" ? doc.description : "",
        visibility,
        results_visibility: resultsVisibility,
        require_full_ballot: requireFullBallot,
        categories,
    };
}

// Build the help-content shown when the user clicks the "?" next to the
// import card. Documents required/optional fields and allowed enum values.
function importHelpContent() {
    const code = (s) => el("code", {}, s);
    function field(name, required, type, allowed, dflt) {
        const tags = [
            el("span", { class: "tag " + (required ? "accent" : "subtle") }, required ? "required" : "optional"),
        ];
        return el("div", { class: "help-field" }, [
            el("div", { class: "help-field-head" }, [
                code(name),
                ...tags,
            ]),
            el("p", { class: "muted" }, [
                type,
                allowed ? el("span", {}, [" — allowed: ", ...allowed.flatMap((v, i) => i === 0 ? [code(v)] : [", ", code(v)])]) : null,
                dflt ? el("span", {}, [" — default: ", code(dflt)]) : null,
            ]),
        ]);
    }
    return el("div", { class: "help-list" }, [
        el("p", { class: "modal-body" }, "Upload a JSON file with this shape. Click ", el("strong", {}, "Download template"), " for a working example you can edit."),

        el("h3", { class: "help-section" }, "Top-level fields"),
        field("name", true, "string"),
        field("categories", true, "array (at least 1)"),
        field("description", false, "string"),
        field("visibility", false, "string", ["public", "invite-only"], "invite-only"),
        field("results_visibility", false, "string", ["after_conclusion", "live"], "after_conclusion"),
        field("require_full_ballot", false, "boolean", ["true", "false"], "false"),
        field("lists", false, "object — named option lists you can reuse"),

        el("h3", { class: "help-section" }, "Each category needs"),
        field("name", true, "string"),
        field("options", true, "string (a list name from \"lists\") OR array of strings (≥ 2)"),
        field("description", false, "string — shown under the category name when voting"),
    ]);
}

// ---------- Modal dialog ----------

// Returns a Promise<boolean> — resolves true if confirmed, false if cancelled.
// `body` may be a string (wrapped in <p>) or a DOM node (appended as-is).
// `infoOnly` hides the Cancel button — used for help/info popups.
function dialog({ eyebrow = "Confirm", title, body, confirm: confirmLabel = "Confirm", danger = false, infoOnly = false } = {}) {
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

        const cancelBtn = infoOnly ? null : el("button", {
            class: "secondary",
            onClick: () => { document.removeEventListener("keydown", onKey); close(false); },
        }, "Cancel");

        let bodyNode = null;
        if (body instanceof Node) bodyNode = body;
        else if (typeof body === "string" && body) bodyNode = el("p", { class: "modal-body" }, body);

        backdrop.appendChild(el("div", { class: "modal" }, [
            el("p", { class: "modal-eyebrow" }, eyebrow),
            el("h2", { class: "modal-title" }, title),
            bodyNode,
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
        // No `html:` escape hatch here on purpose: it was an unused innerHTML
        // sink, and the session token in localStorage makes any XSS a full
        // account takeover. Build nodes and pass text children instead.
        if (k === "class") node.className = v;
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

// ---------- Loading skeletons ----------
//
// Shape-only stand-ins for a view's layout while its data is in flight. No
// spinner: the bars pulse quietly and the page keeps its single entrance
// animation for the real content.
//
// Only routes that actually fetch declare one. The placeholder is held back
// by SKELETON_DELAY_MS so a fast response never flashes one — a skeleton that
// appears and vanishes in 30ms reads as a glitch, not as feedback.
const SKELETON_DELAY_MS = 160;

const skBar = (width, extra = "") =>
    el("div", { class: "sk-bar" + (extra ? " " + extra : ""), style: `width:${width};` });

const skCard = (children) => el("div", { class: "card sk-card" }, children);

const SKELETONS = {
    // Event/profile lists: heading, then a stack of event cards.
    list: () => [
        el("div", { class: "sk-head" }, [skBar("26%", "sk-eyebrow"), skBar("52%", "sk-title")]),
        ...Array.from({ length: 3 }, () => skCard([
            skBar("46%", "sk-line-lg"),
            skBar("72%"),
            el("div", { class: "sk-tags" }, [skBar("64px", "sk-tag"), skBar("92px", "sk-tag")]),
        ])),
    ],
    // Event detail: title block with its tag row, then category cards.
    detail: () => [
        el("div", { class: "sk-head" }, [
            skBar("20%", "sk-eyebrow"),
            skBar("58%", "sk-title"),
            skBar("78%"),
            el("div", { class: "sk-tags" }, [skBar("70px", "sk-tag"), skBar("118px", "sk-tag"), skBar("58px", "sk-tag")]),
        ]),
        ...Array.from({ length: 2 }, () => skCard([
            skBar("38%", "sk-line-lg"),
            el("div", { class: "sk-rows" }, [skBar("100%", "sk-row"), skBar("100%", "sk-row")]),
        ])),
    ],
    // Results: heading, then tally rows over their bar tracks.
    results: () => [
        el("div", { class: "sk-head" }, [skBar("20%", "sk-eyebrow"), skBar("48%", "sk-title")]),
        skCard([26, 34, 30, 22].map((label, i) => el("div", { class: "sk-result" }, [
            skBar(`${label}%`),
            skBar(`${[84, 62, 45, 28][i]}%`, "sk-track"),
        ]))),
    ],
};

// One top-level node, so the skeleton occupies a single slot in the page's
// entrance stagger rather than re-running it per placeholder card.
function skeleton(kind) {
    return el("div", { class: "skeleton", "aria-hidden": "true" }, (SKELETONS[kind] || SKELETONS.list)());
}

// ---------- Router ----------

const routes = [
    { pattern: /^#?\/?$/, view: viewHome },
    { pattern: /^#?\/login$/, view: viewLogin, guestOnly: true },
    { pattern: /^#?\/register$/, view: viewRegister, guestOnly: true },
    { pattern: /^#?\/auth\/google\/([^/]+)$/, view: viewGoogleCallback, params: ["code"] },
    { pattern: /^#?\/events$/, view: viewEvents, requireAuth: true, skeleton: "list" },
    { pattern: /^#?\/events\/new$/, view: viewCreateEvent, requireAuth: true },
    { pattern: /^#?\/events\/(\d+)\/results\/(\d+)$/, view: viewResults, params: ["eventId", "categoryId"], skeleton: "results" },
    { pattern: /^#?\/events\/(\d+)\/results$/, view: viewAllResults, params: ["eventId"], skeleton: "results" },
    { pattern: /^#?\/events\/(\d+)\/edit$/, view: viewEditEvent, params: ["eventId"], requireAuth: true, skeleton: "detail" },
    { pattern: /^#?\/events\/(\d+)$/, view: viewEvent, params: ["eventId"], skeleton: "detail" },
    { pattern: /^#?\/invitations\/([^/]+)$/, view: viewRedeemInvitation, params: ["token"], requireAuth: true },
    { pattern: /^#?\/profile$/, view: viewProfile, requireAuth: true, skeleton: "list" },
];

function navigate(hash) {
    if (location.hash !== hash) location.hash = hash; else router();
}

// Bumped on every navigation so a pending skeleton from a superseded route
// can tell it is stale and skip painting over whatever replaced it.
let navSeq = 0;

async function router() {
    const hash = location.hash || "#/";
    const seq = ++navSeq;
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
        // The mirror of requireAuth: a signed-in visitor has no business on the
        // login or register page, and landing there from a stale tab, a
        // bookmark or the back button is confusing rather than harmful. Bounce
        // silently — there is nothing for them to be told.
        if (r.guestOnly && auth.token) {
            navigate("#/events");
            return;
        }
        // Marks the home route so CSS can drop the topbar's auth buttons there
        // on narrow screens — the hero already offers the same two actions, and
        // on a phone the pair of them filled most of the first viewport.
        document.body.classList.toggle("is-home", r.view === viewHome);
        // And the events list, where the EVENTS tab links to the page you are
        // already on. This has to be an exact route match: setActiveNav marks
        // the tab active for anything under #/events, so keying off .active
        // would also hide it on an event's own page — the one place it is the
        // only way back to the list.
        document.body.classList.toggle("is-events", r.view === viewEvents);
        let skeletonTimer = null;
        if (r.skeleton) {
            $("main").setAttribute("aria-busy", "true");
            skeletonTimer = setTimeout(() => {
                if (seq === navSeq) render(skeleton(r.skeleton));
            }, SKELETON_DELAY_MS);
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
        } finally {
            // Runs before any pending timer fires, so a view that resolves
            // quickly never gets painted over by its own skeleton.
            clearTimeout(skeletonTimer);
            $("main").removeAttribute("aria-busy");
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
            el("h1", { class: "wordmark" }, "Events"),
        ]),
        el("p", { class: "tagline" },
            "Host an evening of categories, nominees, and ceremony — or join the audience and cast your votes."),
        ornament,
        el("div", { class: "hero-actions" }, ctas),
    ]);

    render(hero);
}

// The Google "G", built here rather than through svg() because that helper is
// for the line icons: it forces stroke="currentColor" and fill="none", and this
// mark is filled in four fixed brand colours. Google's brand terms require it
// be shown unmodified, so it deliberately does not inherit the theme's ink
// colour the way every other icon in this file does.
function googleMark() {
    const ns = "http://www.w3.org/2000/svg";
    const node = document.createElementNS(ns, "svg");
    node.setAttribute("viewBox", "0 0 48 48");
    node.setAttribute("class", "google-mark");
    node.setAttribute("aria-hidden", "true");
    const paths = [
        ["#4285F4", "M45.12 24.5c0-1.56-.14-3.06-.4-4.5H24v8.51h11.84c-.51 2.75-2.06 5.08-4.39 6.64v5.52h7.11c4.16-3.83 6.56-9.47 6.56-16.17z"],
        ["#34A853", "M24 46c5.94 0 10.92-1.97 14.56-5.33l-7.11-5.52c-1.97 1.32-4.49 2.1-7.45 2.1-5.73 0-10.58-3.87-12.31-9.07H4.34v5.7C7.96 41.07 15.4 46 24 46z"],
        ["#FBBC05", "M11.69 28.18C11.25 26.86 11 25.45 11 24s.25-2.86.69-4.18v-5.7H4.34C2.85 17.09 2 20.45 2 24s.85 6.91 2.34 9.88l7.35-5.7z"],
        ["#EA4335", "M24 10.75c3.23 0 6.13 1.11 8.41 3.29l6.31-6.31C34.91 4.18 29.93 2 24 2 15.4 2 7.96 6.93 4.34 14.12l7.35 5.7c1.73-5.2 6.58-9.07 12.31-9.07z"],
    ];
    for (const [fill, d] of paths) {
        const path = document.createElementNS(ns, "path");
        path.setAttribute("fill", fill);
        path.setAttribute("d", d);
        node.appendChild(path);
    }
    return node;
}

// A plain link rather than a fetch: the browser has to *navigate* to Google,
// and the server sets a state cookie on the way out that a background request
// would throw away. Returns null when the server has no Google client
// configured, and render()/el() both skip nulls.
function googleSignIn() {
    if (!signInMethods.google) return null;
    return el("div", { class: "oauth" }, [
        el("p", { class: "oauth-divider muted" }, "or"),
        el("a", { class: "btn secondary oauth-google", href: "/auth/google/login" }, [
            googleMark(),
            el("span", {}, "Continue with Google"),
        ]),
    ]);
}

// Where Google's redirect lands. The URL carries a one-time code, not a
// session token, so nothing durable is left behind in browser history.
async function viewGoogleCallback({ code }) {
    document.querySelector("main").classList.add("narrow");

    if (code === "error" || code === "error-email") {
        toast(code === "error-email"
            ? "That email already belongs to a password account — log in with your password instead."
            : "Google sign-in failed. Please try again.", "error");
        navigate("#/login");
        return;
    }

    render(el("p", { class: "muted" }, "Finishing sign-in…"));
    try {
        const res = await API.googleExchange(code);
        auth.set(res.token, res.user);
        renderTopbar();
        toast(`Welcome, ${res.user.username}`, "success");
        navigate("#/events");
    } catch (err) {
        toast(err.message || "Google sign-in failed", "error");
        navigate("#/login");
    }
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
        googleSignIn(),
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

// ---------- Ballot editor ----------

// The category-and-options builder behind both "host an event" and "edit
// event". The only difference between the two is that an edit is seeded with
// the categories already stored, each carrying its id — the server then renames
// that row in place instead of replacing it, which is what lets votes already
// cast on an option survive the edit.
//
// `locked` carries the ids the server will refuse to delete because votes
// already point at them (see viewEditEvent): their remove buttons are disabled
// rather than left to fail on save.
//
// Returns { container, addCategory, collect }. collect() throws an Error whose
// message is written to be shown to the user.
function ballotEditor(seedCategories = [], locked = { categories: new Set(), options: new Set() }) {
    let categoryCount = 0;
    const container = el("div", { id: "categories" });

    // `seed` may carry an id (an existing option) and a name.
    function makeOptionRow(seed = {}) {
        const input = el("input", {
            class: "option-name",
            placeholder: "Option name",
            required: true,
            value: seed.name ?? "",
        });
        if (seed.id != null) input.dataset.optionId = String(seed.id);
        const isLocked = seed.id != null && locked.options.has(seed.id);
        const row = el("div", { class: "option-row" });
        row.appendChild(input);
        row.appendChild(el("button", {
            type: "button",
            class: "secondary",
            disabled: isLocked,
            title: isLocked ? "This option has votes — it can be renamed, but not removed" : "Remove this option",
            onClick: () => row.remove(),
        }, "×"));
        return row;
    }

    // Build a category block. `seed` optionally fills in the id, name,
    // description and options. "Duplicate" uses it to clone a category's
    // options under a blank name, so repeated nominee lists only get typed
    // once — deliberately without ids, since a duplicate is a new category
    // rather than a second view of the same one.
    function makeCategoryBlock(seed = {}) {
        const idx = ++categoryCount;
        const optionsContainer = el("div", { class: "options" });

        const duplicate = () => {
            const options = Array.from(optionsContainer.querySelectorAll(".option-name"))
                .map((i) => ({ name: i.value.trim() }))
                .filter((o) => o.name);
            const clone = makeCategoryBlock({ name: "", options });
            block.after(clone);
            clone.querySelector("input[name^='cat-']").focus();
        };

        const block = el("div", { class: "category-block" }, [
            el("div", { class: "card-row" }, [
                el("strong", {}, `Category ${idx}`),
                el("span", { class: "row-actions" }, [
                    el("button", {
                        type: "button",
                        class: "link",
                        title: "Copy these options into a new category with a blank name",
                        onClick: duplicate,
                    }, "Duplicate"),
                    el("button", {
                        type: "button",
                        class: "link",
                        disabled: seed.id != null && locked.categories.has(seed.id),
                        title: seed.id != null && locked.categories.has(seed.id)
                            ? "This category has votes — it can be renamed, but not removed"
                            : "Remove this category",
                        onClick: () => block.remove(),
                    }, "Remove"),
                ]),
            ]),
            el("label", {}, ["Name", el("input", { name: `cat-${idx}-name`, required: true, placeholder: "e.g. Game of the Year", value: seed.name ?? "" })]),
            el("label", {}, ["Description", el("input", { class: "category-description", placeholder: "Optional — e.g. Indie or AA, released this year", value: seed.description ?? "" })]),
            el("p", { class: "eyebrow", style: "margin-top:8px;" }, "Options"),
            optionsContainer,
            el("button", {
                type: "button",
                class: "secondary",
                onClick: () => optionsContainer.appendChild(makeOptionRow()),
            }, "+ Add option"),
        ]);
        if (seed.id != null) block.dataset.categoryId = String(seed.id);

        const options = seed.options?.length ? seed.options : [{}, {}];
        for (const option of options) optionsContainer.appendChild(makeOptionRow(option));
        return block;
    }

    function addCategory() {
        container.appendChild(makeCategoryBlock());
    }

    // Reads the form back out. Ids ride along as `undefined` when the row is
    // new, and JSON.stringify drops those — which is exactly how the server
    // tells "insert this" from "rename that".
    function collect() {
        const categories = [];
        for (const block of container.querySelectorAll(".category-block")) {
            const name = block.querySelector("input[name^='cat-']").value.trim();
            if (!name) continue;
            const description = block.querySelector(".category-description").value.trim();
            const options = Array.from(block.querySelectorAll(".option-name"))
                .map((input) => ({
                    id: input.dataset.optionId ? Number(input.dataset.optionId) : undefined,
                    name: input.value.trim(),
                }))
                .filter((option) => option.name);
            if (options.length < 2) throw new Error(`Category "${name}" needs at least 2 options`);
            categories.push({
                id: block.dataset.categoryId ? Number(block.dataset.categoryId) : undefined,
                name,
                description,
                options,
            });
        }
        if (categories.length === 0) throw new Error("Add at least one category");
        return categories;
    }

    for (const category of seedCategories) container.appendChild(makeCategoryBlock(category));
    if (!container.children.length) addCategory();

    return { container, addCategory, collect };
}

// ---------- Create event ----------

async function viewCreateEvent() {
    document.querySelector("main").classList.add("wide");

    const editor = ballotEditor();

    const form = el("form", {
        class: "wide",
        onSubmit: async (e) => {
            e.preventDefault();
            const name = form.elements["name"].value.trim();
            const description = form.elements["description"].value.trim();
            const visibility = form.elements["visibility"].value;
            const resultsVisibility = form.elements["results_visibility"].value;
            const requireFullBallot = form.elements["require_full_ballot"].checked;
            if (!name) { toast("Event name required", "error"); return; }
            let categories;
            try {
                // Creating an event has no rows to keep, so the ids the editor
                // tracks are dropped and each category takes plain option names.
                categories = editor.collect().map((cat) => ({
                    name: cat.name,
                    description: cat.description,
                    options: cat.options.map((opt) => opt.name),
                }));
            } catch (err) {
                toast(err.message, "error");
                return;
            }
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
        editor.container,
        el("div", { class: "button-row" }, [
            el("button", { type: "button", class: "secondary", onClick: editor.addCategory }, "+ Add category"),
        ]),
        el("div", { class: "button-row" }, [
            el("button", { type: "submit" }, "Create event"),
            el("a", { class: "btn secondary", href: "#/events" }, "Cancel"),
        ]),
    ]);

    // Hidden file input wired to the visible "Choose file" button.
    const fileInput = el("input", {
        type: "file",
        accept: "application/json,.json",
        style: "display:none;",
        onChange: async (e) => {
            const file = e.target.files && e.target.files[0];
            e.target.value = "";
            if (!file) return;
            let text;
            try {
                text = await file.text();
            } catch (err) {
                toast("Could not read file", "error");
                return;
            }
            let payload;
            try {
                payload = parseEventImport(text);
            } catch (err) {
                toast(err.message, "error");
                return;
            }
            try {
                const created = await API.createEvent(payload);
                toast(`Imported "${created.name}"`, "success");
                navigate(`#/events/${created.id}`);
            } catch (err) {
                toast(err.message || "Failed to create event", "error");
            }
        },
    });

    const helpBtn = el("button", {
        type: "button",
        class: "help-button",
        "aria-label": "Show file format reference",
        title: "Show file format reference",
        onClick: () => dialog({
            eyebrow: "File format",
            title: "Import schema reference",
            body: importHelpContent(),
            infoOnly: true,
            confirm: "Got it",
        }),
    }, "?");

    const importCard = el("section", { class: "card import-card" }, [
        el("p", { class: "eyebrow" }, "Bulk import"),
        el("div", { class: "import-card-head" }, [
            el("h2", {}, "Import from a file"),
            helpBtn,
        ]),
        el("p", { class: "muted" },
            "Have many categories? Upload a JSON file to create the entire event in one step. " +
            "Define reusable option lists once and reference them from each category."),
        el("div", { class: "button-row" }, [
            el("button", { type: "button", onClick: () => fileInput.click() }, "Choose JSON file"),
            el("a", { class: "btn secondary", href: "/event-template.json", download: "event-template.json" }, "Download template"),
        ]),
        fileInput,
    ]);

    render(
        el("p", { class: "eyebrow" }, "New ceremony"),
        el("h1", {}, "Host an event."),
        importCard,
        el("p", { class: "muted divider-note" }, "— or fill in the form below —"),
        form,
    );
}

// ---------- Edit event ----------

// The host's second pass over an event that already exists. The form is the
// create form seeded with what is stored, and it submits the event as it
// should end up — categories and options left out are removed.
//
// Two things it deliberately cannot do, both for the same reason: an option
// that has been voted on can be renamed but not deleted, and an option cannot
// be moved to another category. Votes point at these rows, and either move
// would quietly rewrite a tally rather than correct a mistake. The server
// refuses both; the note below says so before the host loses their edits to a
// 409.
async function viewEditEvent({ eventId }) {
    document.querySelector("main").classList.add("wide");

    const event = await API.getEvent(eventId);
    if (!auth.user || event.host_id !== auth.user.id) {
        toast("Only the host can edit this event", "error");
        navigate(`#/events/${eventId}`);
        return;
    }

    // Which rows the server will refuse to delete. The host can always read
    // an event's results, and that is where the per-option tallies live — so
    // the form can disable those remove buttons up front instead of letting
    // the host rebuild a ballot only to lose the save to a 409. Best-effort:
    // if the tallies can't be fetched the buttons stay live and the server
    // still refuses the removal.
    const locked = { categories: new Set(), options: new Set() };
    try {
        const results = await API.getAllResults(eventId);
        for (const cat of results.categories || []) {
            if (cat.total_votes > 0) locked.categories.add(cat.category_id);
            for (const result of cat.results || []) {
                if (result.votes > 0) locked.options.add(result.option_id);
            }
        }
    } catch {
        // Nothing locked; saving still can't drop a voted row.
    }

    const editor = ballotEditor(event.categories || [], locked);

    const form = el("form", {
        class: "wide",
        onSubmit: async (e) => {
            e.preventDefault();
            const name = form.elements["name"].value.trim();
            if (!name) { toast("Event name required", "error"); return; }

            let categories;
            try {
                categories = editor.collect();
            } catch (err) {
                toast(err.message, "error");
                return;
            }

            try {
                await API.updateEvent(event.id, {
                    name,
                    description: form.elements["description"].value.trim(),
                    visibility: form.elements["visibility"].value,
                    results_visibility: form.elements["results_visibility"].value,
                    require_full_ballot: form.elements["require_full_ballot"].checked,
                    categories,
                });
                toast("Changes saved", "success");
                navigate(`#/events/${event.id}`);
            } catch (err) {
                toast(err.message || "Failed to save changes", "error");
            }
        },
    }, [
        el("label", {}, ["Event name", el("input", { name: "name", required: true, value: event.name })]),
        el("label", {}, ["Description", el("textarea", { name: "description" }, event.description || "")]),
        el("label", {}, [
            "Visibility",
            el("select", { name: "visibility" }, [
                el("option", { value: "invite-only", selected: event.visibility !== "public" }, "Invite-only"),
                el("option", { value: "public", selected: event.visibility === "public" }, "Public"),
            ]),
        ]),
        el("label", {}, [
            "Results visibility",
            el("select", { name: "results_visibility" }, [
                el("option", { value: "after_conclusion", selected: event.results_visibility !== "live" }, "After event closes (default)"),
                el("option", { value: "live", selected: event.results_visibility === "live" }, "Live"),
            ]),
        ]),
        el("label", { class: "checkbox-label" }, [
            el("input", { type: "checkbox", name: "require_full_ballot", checked: !!event.require_full_ballot }),
            "Require voters to fill all categories before submitting",
        ]),
        el("h2", {}, "Categories"),
        el("p", { class: "muted" },
            "Renaming a category or an option keeps the votes already cast on it. " +
            "Anything that has been voted on can no longer be removed, so its × is greyed out."),
        editor.container,
        el("div", { class: "button-row" }, [
            el("button", { type: "button", class: "secondary", onClick: editor.addCategory }, "+ Add category"),
        ]),
        el("div", { class: "button-row" }, [
            el("button", { type: "submit" }, "Save changes"),
            el("a", { class: "btn secondary", href: `#/events/${event.id}` }, "Cancel"),
        ]),
    ]);

    render(
        el("p", { class: "eyebrow" }, "Edit event"),
        el("h1", {}, event.name),
        !event.is_active
            ? el("p", { class: "muted" }, "This event is closed. Edits still apply, but nobody can vote on them.")
            : null,
        form,
    );
}

// ---------- Event detail ----------

// Coarse "time until" for invitation expiry — days beyond 48h, hours beyond
// 90 min, minutes below that. Returns null when the moment has passed.
function formatTimeLeft(iso) {
    const ms = new Date(iso).getTime() - Date.now();
    if (Number.isNaN(ms) || ms <= 0) return null;
    const minutes = Math.round(ms / 60000);
    if (minutes < 90) return `${Math.max(minutes, 1)} min`;
    const hours = Math.round(minutes / 60);
    if (hours < 48) return `${hours} hours`;
    return `${Math.round(hours / 24)} days`;
}

// Build the host-only "Invitations" card. Self-fetches the invitation list,
// re-renders on create/revoke. Returns a DOM section node.
function buildInvitationsCard(eventId) {
    const listMount = el("div", { class: "invitation-list" });

    function shortToken(token) {
        if (typeof token !== "string" || token.length <= 14) return token;
        return token.slice(0, 8) + "…" + token.slice(-4);
    }

    function renderList(invites) {
        clear(listMount);
        if (!invites.length) {
            listMount.appendChild(el("p", { class: "muted" }, "No invitations yet — create a link to invite someone."));
            return;
        }
        invites.forEach((inv) => {
            // max_uses absent means the link is unlimited — the one a host
            // posts in a group chat. Otherwise it admits that many people.
            const unlimited = inv.max_uses == null;
            const maxUses = unlimited ? Infinity : inv.max_uses;
            const uses = inv.uses || 0;
            const usedUp = uses >= maxUses;
            const timeLeft = inv.expires_at ? formatTimeLeft(inv.expires_at) : null;
            const isExpired = !usedUp && inv.expires_at && timeLeft === null;
            const usable = !usedUp && !isExpired;
            const link = `${location.origin}/#/invitations/${inv.token}`;

            // Who came through the link, for the tag's tooltip.
            const joiners = (inv.redemptions || []).map((r) => `@${r.username}`).join(", ");

            const kindTag = unlimited
                ? el("span", { class: "tag accent" }, "unlimited")
                : maxUses > 1
                    ? el("span", { class: "tag subtle", title: joiners || undefined }, `${uses} of ${maxUses} used`)
                    : null;

            let statusTag;
            if (usedUp && maxUses === 1) {
                statusTag = el("span", { class: "tag subtle" }, `redeemed by ${joiners || "someone"}`);
            } else if (isExpired) {
                statusTag = el("span", { class: "tag danger" }, "expired");
            } else if (usedUp) {
                statusTag = el("span", { class: "tag subtle" }, "used up");
            } else if (unlimited && uses > 0) {
                statusTag = el("span", { class: "tag success", title: joiners },
                    uses === 1 ? "1 person joined" : `${uses} people joined`);
            } else {
                statusTag = el("span", { class: "tag success" }, "outstanding");
            }

            const expiryTag = usable && timeLeft
                ? el("span", { class: "tag subtle" }, `expires in ${timeLeft}`)
                : null;

            const actions = el("div", { class: "button-row" }, [
                usable ? el("button", {
                    class: "secondary",
                    onClick: async () => {
                        try {
                            await navigator.clipboard.writeText(link);
                            toast("Invite link copied", "success");
                        } catch {
                            toast(link, "");
                        }
                    },
                }, "Copy link") : null,
                el("button", {
                    class: "danger",
                    onClick: async () => {
                        const ok = await dialog({
                            eyebrow: "Host action",
                            title: "Revoke this invitation?",
                            body: uses > 0
                                ? "Nobody else will be able to join through this link. Anyone who already joined stays a member."
                                : "Anyone holding this link will no longer be able to join.",
                            confirm: "Revoke",
                            danger: true,
                        });
                        if (!ok) return;
                        try {
                            await API.revokeInvitation(eventId, inv.token);
                            toast("Invitation revoked", "success");
                            reload();
                        } catch (err) {
                            toast(err.message || "Failed to revoke", "error");
                        }
                    },
                }, "Revoke"),
            ]);

            const row = el("div", { class: "invitation-row" }, [
                el("div", { class: "invitation-row-main" }, [
                    el("code", { class: "invitation-token" }, shortToken(inv.token)),
                    kindTag,
                    statusTag,
                    expiryTag,
                ]),
                actions,
            ]);
            listMount.appendChild(row);
        });
    }

    async function reload() {
        try {
            const invites = await API.listInvitations(eventId);
            renderList(invites);
        } catch (err) {
            clear(listMount);
            listMount.appendChild(el("p", { class: "muted" }, err.message || "Failed to load invitations."));
        }
    }

    const expirySelect = el("select", { "aria-label": "Invitation expiry" }, [
        el("option", { value: "" }, "No expiry"),
        el("option", { value: "1" }, "Expires in 1 hour"),
        el("option", { value: "24" }, "Expires in 24 hours"),
        el("option", { value: "72" }, "Expires in 3 days"),
        el("option", { value: "168" }, "Expires in 7 days"),
    ]);

    // Single use is the default: a link that admits everyone who reads it is
    // the right tool for a group chat and the wrong one to hand to one person.
    const usesSelect = el("select", { "aria-label": "How many people this invitation admits" }, [
        el("option", { value: "1" }, "Single use"),
        el("option", { value: "unlimited" }, "Unlimited — share in a group"),
    ]);

    const createBtn = el("button", {
        class: "secondary",
        onClick: async () => {
            try {
                const hours = parseInt(expirySelect.value, 10);
                const unlimited = usesSelect.value === "unlimited";
                // null is how the API spells "no maximum", the same way an
                // absent expiry spells "never expires".
                const body = { max_uses: unlimited ? null : 1 };
                if (hours) body.expires_in_hours = hours;
                const inv = await API.createInvitation(eventId, body);
                const link = `${location.origin}/#/invitations/${inv.token}`;
                try { await navigator.clipboard.writeText(link); } catch {}
                toast(`${unlimited ? "Unlimited invite" : "Invite"} link copied: ${link}`, "success");
                reload();
            } catch (err) {
                toast(err.message || "Failed to create invite", "error");
            }
        },
    }, "Create invite link");

    const card = el("section", { class: "card" }, [
        el("div", { class: "card-row" }, [
            el("div", {}, [
                el("p", { class: "eyebrow" }, "Invitations"),
                el("h2", { style: "margin:0;" }, "Invite people"),
            ]),
            el("div", { class: "button-row" }, [usesSelect, expirySelect, createBtn]),
        ]),
        listMount,
    ]);

    reload();
    return card;
}

// Timestamps arrive as RFC3339 from the API; the day is all a member row needs.
function formatJoinDate(iso) {
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? "" : d.toLocaleDateString();
}

// Build the host-only "Members" card. Self-fetches the member list; a removal
// re-runs the router because it also moves the member/turnout counts in the
// header. Returns a DOM section node.
function buildMembersCard(eventId) {
    const listMount = el("div", { class: "member-list" });

    function renderList(members) {
        clear(listMount);
        if (!members.length) {
            listMount.appendChild(el("p", { class: "muted" }, "No members yet."));
            return;
        }
        members.forEach((m) => {
            const removeBtn = el("button", {
                class: "danger",
                onClick: async () => {
                    const ok = await dialog({
                        eyebrow: "Host action",
                        title: `Remove @${m.username}?`,
                        body: "They lose access to the event and stop counting towards turnout. Any votes they already cast stay in the results.",
                        confirm: "Remove member",
                        danger: true,
                    });
                    if (!ok) return;
                    try {
                        await API.removeMember(eventId, m.user_id);
                        toast(`Removed @${m.username}`, "success");
                        router();
                    } catch (err) {
                        toast(err.message || "Failed to remove member", "error");
                    }
                },
            }, "Remove");

            listMount.appendChild(el("div", { class: "member-row" }, [
                el("div", { class: "member-row-main" }, [
                    el("span", { class: "option-name" }, `@${m.username}`),
                    m.is_host ? el("span", { class: "tag accent" }, "host") : null,
                    el("span", { class: "muted" }, `joined ${formatJoinDate(m.joined_at)}`),
                ]),
                m.is_host ? null : el("div", { class: "button-row" }, [removeBtn]),
            ]));
        });
    }

    async function reload() {
        try {
            renderList(await API.listMembers(eventId));
        } catch (err) {
            clear(listMount);
            listMount.appendChild(el("p", { class: "muted" }, err.message || "Failed to load members."));
        }
    }

    const card = el("section", { class: "card" }, [
        el("div", { class: "card-row" }, [
            el("div", {}, [
                el("p", { class: "eyebrow" }, "Members"),
                el("h2", { style: "margin:0;" }, "Who has joined"),
            ]),
        ]),
        listMount,
    ]);

    reload();
    return card;
}

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

    // Non-members of an invite-only event get a redacted payload from the API
    // (no description, categories, or counts) — skip the tags those feed.
    const walled = event.visibility === "invite-only" && !event.is_member && !isHost;

    const memberCount = event.member_count || 0;
    const voterCount = event.voter_count || 0;
    const tags = [
        el("span", { class: "tag " + (event.visibility === "public" ? "subtle" : "accent") }, event.visibility),
        !walled ? el("span", { class: "tag subtle" }, event.results_visibility === "live" ? "live results" : "results after close") : null,
        el("span", { class: event.is_active ? "tag success" : "tag danger" }, event.is_active ? "open" : "closed"),
        !walled ? el("span", { class: "tag subtle" }, `${memberCount} member${memberCount === 1 ? "" : "s"}`) : null,
        !walled ? el("span", { class: "tag subtle" }, `${voterCount} of ${memberCount} voted`) : null,
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
            isHost ? el("a", {
                class: "btn secondary",
                href: `#/events/${event.id}/edit`,
            }, "Edit event") : null,
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

    const invitationsCard = isHost && event.is_active ? buildInvitationsCard(event.id) : null;
    // Unlike invitations, the roster stays visible after the event closes — a
    // host still wants to see who took part.
    const membersCard = isHost ? buildMembersCard(event.id) : null;

    // Non-member trying to view an invite-only event — show a wall instead of
    // categories. Must come before the empty-categories check: the redacted
    // payload has no categories, and this is the reason why.
    if (walled) {
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

    if (categories.length === 0) {
        render(headerCard, invitationsCard, membersCard, el("p", { class: "muted" }, "No categories."));
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
            cat.description ? el("p", { class: "muted" }, cat.description) : null,
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

            const votes = Object.entries(drafts).map(([catId, optId]) => ({
                category_id: Number(catId),
                option_id: Number(optId),
            }));

            try {
                await API.submitBallot(event.id, votes);
                toast("Votes submitted", "success");
            } catch (err) {
                // The ballot is all-or-nothing now, so there is no partial
                // state to reconcile — report why and let them retry.
                toast(err.message || "Could not submit votes", "error");
                submitBtn.disabled = false;
                submitBtn.textContent = "Submit votes";
                return;
            }
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
        invitationsCard,
        membersCard,
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
        // Following a link to an event you are already in is not a failure —
        // the host clicking their own link, or a second tap on the group chat
        // message. Say so and take them there.
        if (res.already_member) {
            toast("You are already a member of this event", "");
        } else {
            toast("Joined event via invitation", "success");
        }
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

// Renaming yourself. The name a Google sign-in generates is derived from the
// email address, which is rarely what anyone wants to be called, so this is
// offered to every account regardless of how it signs in.
async function promptRename() {
    const current = auth.user?.username || "";
    const input = el("input", {
        value: current,
        maxlength: "32",
        "aria-label": "Your name",
        onKeyDown: (e) => {
            // The dialog is not a form, so Enter would otherwise do nothing.
            if (e.key !== "Enter") return;
            e.preventDefault();
            e.target.closest(".modal").querySelector(".button-row button").click();
        },
    });

    const pending = dialog({
        eyebrow: "Profile",
        title: "Change your name",
        body: el("div", {}, [
            el("p", { class: "modal-body" },
                "This is the name other people see on member lists, invitations and results."),
            el("label", {}, ["Name", input]),
        ]),
        confirm: "Save name",
    });
    // dialog() focuses its confirm button as it opens; the field is the thing
    // to type in, so take the focus back before waiting on the answer.
    input.focus();
    input.select();
    if (!await pending) return;

    const username = input.value.trim();
    if (!username || username === current) return;

    try {
        const user = await API.updateProfile({ username });
        auth.set(auth.token, user);
        renderTopbar();
        toast("Name updated", "success");
        router();
    } catch (err) {
        toast(err.message || "Could not change your name", "error");
    }
}

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
        el("div", { class: "button-row profile-actions" }, [
            el("button", { class: "secondary", onClick: promptRename }, "Change name"),
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
    // Started before the session check, not after: neither depends on the
    // other, so there is no reason to pay for them in series.
    const methods = API.authConfig()
        .then((cfg) => { signInMethods = cfg; })
        .catch(() => { /* leave Google hidden if the server cannot be asked */ });

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
    await methods;
    renderTopbar();
    router();
});
