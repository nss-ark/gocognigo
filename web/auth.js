// === GoCognigo — Firebase Authentication ===

// Firebase config is loaded from /api/auth/config endpoint
let firebaseApp = null;
let firebaseAuth = null;
let currentUser = null;
let authIdToken = null;
let tokenRefreshInterval = null;

// Override global fetch to auto-attach auth token
const _originalFetch = window.fetch;
window.fetch = function (url, options = {}) {
    // Only attach token to our own API calls
    if (authIdToken && typeof url === 'string' && url.startsWith(API_BASE + '/api/')) {
        options.headers = options.headers || {};
        if (options.headers instanceof Headers) {
            options.headers.set('Authorization', 'Bearer ' + authIdToken);
        } else {
            options.headers['Authorization'] = 'Bearer ' + authIdToken;
        }
    }
    return _originalFetch.call(this, url, options);
};

// Initialize Firebase Auth
async function initAuth() {
    try {
        const res = await _originalFetch(`${API_BASE}/api/auth/config`);
        if (!res.ok) {
            // Auth not configured — run without auth (local dev mode)
            console.log('Auth not configured, running in local mode');
            showApp();
            return;
        }
        const config = await res.json();
        if (!config.apiKey) {
            console.log('Auth not configured, running in local mode');
            showApp();
            return;
        }

        // Dynamically load Firebase SDK
        await loadScript('https://www.gstatic.com/firebasejs/10.14.1/firebase-app-compat.js');
        await loadScript('https://www.gstatic.com/firebasejs/10.14.1/firebase-auth-compat.js');

        firebaseApp = firebase.initializeApp(config);
        firebaseAuth = firebase.auth();

        // Listen for auth state changes
        firebaseAuth.onAuthStateChanged(async (user) => {
            if (user) {
                currentUser = user;
                authIdToken = await user.getIdToken();
                // Refresh token every 50 minutes (tokens expire in 1 hour)
                if (tokenRefreshInterval) clearInterval(tokenRefreshInterval);
                tokenRefreshInterval = setInterval(async () => {
                    if (currentUser) {
                        authIdToken = await currentUser.getIdToken(true);
                    }
                }, 50 * 60 * 1000);

                showApp();
                updateAuthUI();
            } else {
                currentUser = null;
                authIdToken = null;
                showLoginScreen();
            }
        });
    } catch (e) {
        console.error('Auth init failed, running in local mode:', e);
        showApp();
    }
}

function loadScript(src) {
    return new Promise((resolve, reject) => {
        // Check if already loaded
        if (document.querySelector(`script[src="${src}"]`)) {
            resolve();
            return;
        }
        const script = document.createElement('script');
        script.src = src;
        script.onload = resolve;
        script.onerror = reject;
        document.head.appendChild(script);
    });
}

function showLoginScreen() {
    document.getElementById('authScreen').classList.remove('hidden');
    document.getElementById('appContainer').classList.add('hidden');
}

function showApp() {
    document.getElementById('authScreen').classList.add('hidden');
    document.getElementById('appContainer').classList.remove('hidden');
    // In local mode, set a default user for the UI
    if (!currentUser) {
        currentUser = { displayName: 'Local User', email: null, photoURL: null };
    }
    updateAuthUI();
    // Load app data now that auth is confirmed (token is set for Firebase, or local mode)
    loadStats();
    loadProviders();
    loadProjects();
    checkApiKeySetup();
}

function updateAuthUI() {
    const userInfo = document.getElementById('authUserInfo');
    if (!userInfo) return;

    if (currentUser) {
        const name = currentUser.displayName || currentUser.email || 'User';
        const photo = currentUser.photoURL;
        userInfo.innerHTML = `
            <div class="auth-user">
                ${photo ? `<img class="auth-avatar" src="${photo}" alt="" referrerpolicy="no-referrer">` : '<div class="auth-avatar-placeholder"></div>'}
                <span class="auth-name">${escapeHtml(name)}</span>
                <button class="auth-signout-btn" onclick="signOut()" title="Sign out">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <path d="M9 21H5a2 2 0 01-2-2V5a2 2 0 012-2h4"></path>
                        <polyline points="16 17 21 12 16 7"></polyline>
                        <line x1="21" y1="12" x2="9" y2="12"></line>
                    </svg>
                </button>
            </div>`;
        userInfo.classList.remove('hidden');
    } else {
        userInfo.classList.add('hidden');
    }
}

function setAuthBtnLoading(btnId, loading) {
    const btn = document.getElementById(btnId);
    if (!btn) return;
    const spinner = btn.querySelector('.auth-btn-spinner');
    if (loading) {
        btn.classList.add('loading');
        btn.disabled = true;
        if (spinner) spinner.classList.remove('hidden');
    } else {
        btn.classList.remove('loading');
        btn.disabled = false;
        if (spinner) spinner.classList.add('hidden');
    }
}

async function signInWithGoogle() {
    if (!firebaseAuth) return;
    setAuthBtnLoading('googleSignInBtn', true);
    try {
        const provider = new firebase.auth.GoogleAuthProvider();
        await firebaseAuth.signInWithPopup(provider);
    } catch (e) {
        console.error('Google sign-in failed:', e);
        if (e.code !== 'auth/popup-closed-by-user') {
            alert('Sign-in failed: ' + e.message);
        }
    } finally {
        setAuthBtnLoading('googleSignInBtn', false);
    }
}

async function signInWithEmail() {
    if (!firebaseAuth) return;
    const email = document.getElementById('authEmail').value.trim();
    const password = document.getElementById('authPassword').value;
    if (!email || !password) {
        alert('Please enter email and password');
        return;
    }

    setAuthBtnLoading('emailSignInBtn', true);
    try {
        await firebaseAuth.signInWithEmailAndPassword(email, password);
    } catch (e) {
        if (e.code === 'auth/user-not-found' || e.code === 'auth/invalid-credential') {
            // Try to create account
            if (confirm('Account not found. Create a new account?')) {
                try {
                    await firebaseAuth.createUserWithEmailAndPassword(email, password);
                } catch (e2) {
                    alert('Sign-up failed: ' + e2.message);
                }
            }
        } else {
            alert('Sign-in failed: ' + e.message);
        }
    } finally {
        setAuthBtnLoading('emailSignInBtn', false);
    }
}

async function signOut() {
    if (firebaseAuth) {
        try {
            await firebaseAuth.signOut();
        } catch (e) {
            console.error('Sign-out failed:', e);
        }
    } else {
        // In local mode, just clear current user and reload
        currentUser = null;
        location.reload();
    }
}
