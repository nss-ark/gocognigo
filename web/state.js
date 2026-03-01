// === GoCognigo — Shared State ===

const API_BASE = '';

const DEFAULT_BATCH_QUESTIONS = [
    "What are the revenue figures for Meta for Q1, Q2 and Q3?",
    "What was KFIN's revenue in 2021?",
    "What metrics helped CCI determine if the combination would be anticompetitive?",
    "What was the bench in the Eastman Kodak Case?",
    "How many SCOTUS cases are in the set? Name them.",
    "What is the governing law in the NVCA IRA?",
    "If Pristine were to acquire an indian company that had turnover of 1Cr and no assets, would it have to notify the deal to the CCI?"
];

// Load batch questions from localStorage or use defaults
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
