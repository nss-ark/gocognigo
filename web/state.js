// === GoCognigo — Shared State ===

const API_BASE = '';

const DEFAULT_BATCH_QUESTIONS = [];

// Load batch questions from localStorage or start empty
let batchQuestions = JSON.parse(localStorage.getItem('batchQuestions')) || [...DEFAULT_BATCH_QUESTIONS];

// ===== State =====
let currentPhase = 'upload';    // 'upload' | 'processing' | 'chat'
let activeProjectId = null;
let activeConversationId = null;
let projects = [];
let conversations = [];
let uploadedFiles = [];
let convHasBeenNamed = false;

let currentMode = 'single';
let currentProvider = 'anthropic';
let currentModel = '';
let availableProviders = [];
let providerModels = {};
let timerInterval = null;
let ingestPollInterval = null;
let activeQueryController = null;
let loaderTextInterval = null;
