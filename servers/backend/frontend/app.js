// Voting App — minimal vanilla SPA.
// Hash-based routes: #/, #/login, #/register, #/events, #/events/new,
// #/events/:id, #/events/:id/results/:catId, #/invitations/:token

const TOKEN_KEY = "voting.token";
const USER_KEY = "voting.user";

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
        const err = new Error(msg || `HTTP ${res.status}`);
        err.status = res.status;
        throw err;
    }
    return data;
}

const API = {
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
};

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
    nodes.forEach((n) => view.appendChild(n));
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

// ---------- Nav ----------

function renderNav() {
    const nav = $("#nav");
    clear(nav);
    if (auth.token) {
        nav.appendChild(el("a", { href: "#/events" }, "Events"));
        nav.appendChild(el("span", { class: "muted" }, `${auth.user?.username || ""}`));
        nav.appendChild(el("button", {
            class: "secondary",
            onClick: async () => {
                try { await API.logout(); } catch { /* ignore */ }
                auth.clear();
                renderNav();
                navigate("#/login");
            },
        }, "Logout"));
    } else {
        nav.appendChild(el("a", { href: "#/login" }, "Login"));
        nav.appendChild(el("a", { href: "#/register" }, "Register"));
    }
}

// ---------- Router ----------

const routes = [
    { pattern: /^#?\/?$/, view: viewHome },
    { pattern: /^#?\/login$/, view: viewLogin },
    { pattern: /^#?\/register$/, view: viewRegister },
    { pattern: /^#?\/events$/, view: viewEvents },
    { pattern: /^#?\/events\/new$/, view: viewCreateEvent, requireAuth: true },
    { pattern: /^#?\/events\/(\d+)\/results\/(\d+)$/, view: viewResults, params: ["eventId", "categoryId"] },
    { pattern: /^#?\/events\/(\d+)$/, view: viewEvent, params: ["eventId"] },
    { pattern: /^#?\/invitations\/([^/]+)$/, view: viewRedeemInvitation, params: ["token"], requireAuth: true },
];

function navigate(hash) {
    if (location.hash !== hash) location.hash = hash; else router();
}

async function router() {
    const hash = location.hash || "#/";
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
            console.error(err);
            render(el("div", { class: "error-box" }, err.message || String(err)));
        }
        return;
    }
    render(el("div", { class: "empty" }, "Not found."));
}

// ---------- Views ----------

async function viewHome() {
    if (auth.token) { navigate("#/events"); return; }
    render(
        el("h1", {}, "Voting App"),
        el("p", { class: "subtitle" }, "Create awards-show style events, invite people, and vote across categories."),
        el("div", { class: "button-row" }, [
            el("a", { class: "btn", href: "#/login" }, "Log in"),
            el("a", { class: "btn secondary", href: "#/register" }, "Create an account"),
        ]),
    );
}

async function viewLogin() {
    const form = el("form", {
        onSubmit: async (e) => {
            e.preventDefault();
            const username = form.username.value.trim();
            const password = form.password.value;
            if (!username || !password) { toast("Username and password required", "error"); return; }
            try {
                const res = await API.login({ username, password });
                auth.set(res.token, res.user);
                renderNav();
                toast(`Welcome back, ${res.user.username}`, "success");
                navigate("#/events");
            } catch (err) {
                toast(err.message || "Login failed", "error");
            }
        },
    }, [
        el("label", {}, ["Username", el("input", { name: "username", autocomplete: "username", required: true })]),
        el("label", {}, ["Password", el("input", { type: "password", name: "password", autocomplete: "current-password", required: true })]),
        el("div", { class: "button-row" }, [
            el("button", { type: "submit" }, "Log in"),
            el("a", { class: "btn secondary", href: "#/register" }, "Register instead"),
        ]),
    ]);
    render(el("h1", {}, "Log in"), form);
}

async function viewRegister() {
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
                renderNav();
                toast(`Account created — welcome, ${res.user.username}`, "success");
                navigate("#/events");
            } catch (err) {
                toast(err.message || "Registration failed", "error");
            }
        },
    }, [
        el("label", {}, ["Username", el("input", { name: "username", autocomplete: "username", required: true })]),
        el("label", {}, ["Email", el("input", { type: "email", name: "email", autocomplete: "email", required: true })]),
        el("label", {}, ["Password (min 6 chars)", el("input", { type: "password", name: "password", autocomplete: "new-password", required: true })]),
        el("div", { class: "button-row" }, [
            el("button", { type: "submit" }, "Create account"),
            el("a", { class: "btn secondary", href: "#/login" }, "Log in instead"),
        ]),
    ]);
    render(el("h1", {}, "Create account"), form);
}

async function viewEvents() {
    let events;
    try {
        events = await API.listEvents();
    } catch (err) {
        if (err.status === 401) { auth.clear(); renderNav(); navigate("#/login"); return; }
        throw err;
    }
    const header = el("div", { class: "card-row" }, [
        el("div", {}, [
            el("h1", {}, "Events"),
            el("p", { class: "muted" }, auth.token ? "Public events and the events you host or have joined." : "Public events. Log in to see and create more."),
        ]),
        auth.token && el("a", { class: "btn", href: "#/events/new" }, "Create event"),
    ]);
    const cards = (events || []).map((e) => {
        const tags = [
            el("span", { class: `tag ${e.visibility === "public" ? "" : "warning"}` }, e.visibility),
            el("span", { class: "tag muted" }, e.results_visibility === "live" ? "live results" : "results after close"),
            el("span", { class: e.is_active ? "tag success" : "tag danger" }, e.is_active ? "open" : "closed"),
        ];
        return el("a", {
            class: "card",
            href: `#/events/${e.id}`,
            style: "display:block; color:inherit;",
        }, [
            el("h3", {}, e.name),
            e.description ? el("p", { class: "muted" }, e.description) : null,
            el("div", { class: "card-meta" }, tags),
        ]);
    });
    const list = events && events.length
        ? el("div", {}, cards)
        : el("div", { class: "empty" }, "No events yet.");
    render(header, list);
}

async function viewCreateEvent() {
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
            el("h2", {}, "Options"),
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

    render(el("h1", {}, "Create event"), form);
}

async function viewEvent({ eventId }) {
    let event;
    try {
        event = await API.getEvent(eventId);
    } catch (err) {
        if (err.status === 401) { auth.clear(); renderNav(); navigate("#/login"); return; }
        throw err;
    }

    const isHost = auth.user && event.host_id === auth.user.id;

    const tags = [
        el("span", { class: `tag ${event.visibility === "public" ? "" : "warning"}` }, event.visibility),
        el("span", { class: "tag muted" }, event.results_visibility === "live" ? "live results" : "results after close"),
        el("span", { class: event.is_active ? "tag success" : "tag danger" }, event.is_active ? "open" : "closed"),
        isHost ? el("span", { class: "tag" }, "you host") : null,
    ];

    const headerCard = el("div", { class: "card" }, [
        el("h1", {}, event.name),
        event.description ? el("p", {}, event.description) : null,
        el("div", { class: "card-meta" }, tags),
        el("div", { class: "button-row", style: "margin-top:12px;" }, [
            auth.token && event.visibility === "public" && event.is_active
                ? el("button", {
                    class: "secondary",
                    onClick: async () => {
                        try {
                            await API.joinEvent(event.id);
                            toast("Joined event", "success");
                            router();
                        } catch (err) { toast(err.message || "Failed to join", "error"); }
                    },
                }, "Join event")
                : null,
            isHost && event.is_active ? el("button", {
                onClick: async () => {
                    try {
                        const inv = await API.createInvitation(event.id);
                        const link = `${location.origin}/#/invitations/${inv.token}`;
                        await navigator.clipboard?.writeText(link).catch(() => {});
                        toast(`Invite link copied: ${link}`, "success");
                    } catch (err) { toast(err.message || "Failed to create invite", "error"); }
                },
            }, "Create invite link") : null,
            isHost && event.is_active ? el("button", {
                class: "danger",
                onClick: async () => {
                    if (!confirm("Close this event? Voting will stop and results become public to members.")) return;
                    try {
                        await API.closeEvent(event.id);
                        toast("Event closed", "success");
                        router();
                    } catch (err) { toast(err.message || "Failed to close", "error"); }
                },
            }, "Close event") : null,
        ]),
    ]);

    const sections = (event.categories || []).map((cat) => renderCategorySection(event, cat, isHost));
    const categoriesView = sections.length
        ? el("div", {}, sections)
        : el("p", { class: "muted" }, "No categories.");

    render(headerCard, el("h2", {}, "Categories"), categoriesView);
}

function renderCategorySection(event, category, isHost) {
    const section = el("div", { class: "card" }, [
        el("h3", {}, category.name),
    ]);
    const optionsList = el("div", { class: "vote-options" });
    (category.options || []).forEach((opt) => {
        const row = el("div", { class: "vote-option" }, [
            el("span", {}, opt.name),
            auth.token && event.is_active ? el("button", {
                onClick: async () => {
                    try {
                        await API.vote(category.id, opt.id);
                        toast(`Voted: ${opt.name}`, "success");
                        Array.from(optionsList.children).forEach((c) => c.classList.remove("selected"));
                        row.classList.add("selected");
                    } catch (err) {
                        toast(err.message || "Failed to vote", "error");
                    }
                },
            }, "Vote") : null,
        ]);
        optionsList.appendChild(row);
    });
    section.appendChild(optionsList);

    const showResultsLink = !event.is_active || event.results_visibility === "live" || isHost;
    if (showResultsLink) {
        section.appendChild(el("div", { class: "button-row" }, [
            el("a", { class: "btn secondary", href: `#/events/${event.id}/results/${category.id}` }, "View results"),
        ]));
    } else {
        section.appendChild(el("p", { class: "muted" }, "Results will appear after the host closes the event."));
    }
    return section;
}

async function viewResults({ eventId, categoryId }) {
    let results;
    try {
        results = await API.getResults(eventId, categoryId);
    } catch (err) {
        if (err.status === 401) { auth.clear(); renderNav(); navigate("#/login"); return; }
        throw err;
    }
    const total = results.total_votes || 0;
    const sorted = (results.results || []).slice().sort((a, b) => b.votes - a.votes);
    const bars = sorted.map((r) => {
        const pct = total > 0 ? Math.round((r.votes / total) * 100) : 0;
        return el("div", { class: "results-bar" }, [
            el("div", { class: "card-row" }, [
                el("span", {}, r.option_name),
                el("span", { class: "muted" }, `${r.votes} vote${r.votes === 1 ? "" : "s"} (${pct}%)`),
            ]),
            el("div", { class: "results-bar-track" }, [
                el("div", { class: "results-bar-fill", style: `width:${pct}%` }),
            ]),
        ]);
    });
    render(
        el("h1", {}, results.category_name),
        el("p", { class: "muted" }, `${total} total vote${total === 1 ? "" : "s"}`),
        el("div", { class: "card" }, bars.length ? bars : [el("p", { class: "muted" }, "No votes yet.")]),
        el("div", { class: "button-row" }, [
            el("a", { class: "btn secondary", href: `#/events/${eventId}` }, "← Back to event"),
        ]),
    );
}

async function viewRedeemInvitation({ token }) {
    render(el("p", { class: "muted" }, "Redeeming invitation..."));
    try {
        const res = await API.redeemInvitation(token);
        toast("Joined event via invitation", "success");
        navigate(`#/events/${res.event_id}`);
    } catch (err) {
        if (err.status === 401) { auth.clear(); renderNav(); navigate("#/login"); return; }
        render(
            el("h1", {}, "Invitation"),
            el("div", { class: "error-box" }, err.message || "Could not redeem invitation."),
            el("div", { class: "button-row" }, [
                el("a", { class: "btn secondary", href: "#/events" }, "Back to events"),
            ]),
        );
    }
}

// ---------- Boot ----------

window.addEventListener("hashchange", router);
window.addEventListener("DOMContentLoaded", () => {
    renderNav();
    router();
});
