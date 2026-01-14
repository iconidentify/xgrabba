/**
 * xgrabba Admin Events Log
 *
 * Real-time activity monitoring console with SSE updates, filtering,
 * search, and pagination capabilities.
 *
 * Architecture Notes:
 * - Uses Server-Sent Events (SSE) for real-time updates
 * - Event data is normalized through EventManager class
 * - Filters are composable and applied client-side for responsiveness
 * - Pagination uses cursor-based approach for consistency with live updates
 */

(function() {
    'use strict';

    // ==========================================================================
    // Configuration
    // ==========================================================================

    const CONFIG = {
        SSE_ENDPOINT: '/api/events/stream',
        EVENTS_ENDPOINT: '/api/events',
        STATS_ENDPOINT: '/api/events/stats',
        PAGE_SIZE: 50,
        RECONNECT_DELAY: 3000,
        MAX_RECONNECT_ATTEMPTS: 10,
        DEBOUNCE_DELAY: 300,
        TOAST_DURATION: 5000
    };

    // Severity icons as SVG strings
    const SEVERITY_ICONS = {
        info: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/>
        </svg>`,
        success: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z"/>
        </svg>`,
        warning: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/>
        </svg>`,
        error: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"/>
        </svg>`
    };

    // ==========================================================================
    // Utility Functions
    // ==========================================================================

    /**
     * Debounce function execution
     */
    function debounce(fn, delay) {
        let timeoutId;
        return function(...args) {
            clearTimeout(timeoutId);
            timeoutId = setTimeout(() => fn.apply(this, args), delay);
        };
    }

    /**
     * Format timestamp for display
     * Shows relative time for recent events, absolute for older
     */
    function formatTimestamp(isoString) {
        const date = new Date(isoString);
        const now = new Date();
        const diffMs = now - date;
        const diffMins = Math.floor(diffMs / 60000);
        const diffHours = Math.floor(diffMs / 3600000);
        const diffDays = Math.floor(diffMs / 86400000);

        if (diffMins < 1) return 'Just now';
        if (diffMins < 60) return `${diffMins}m ago`;
        if (diffHours < 24) return `${diffHours}h ago`;
        if (diffDays < 7) return `${diffDays}d ago`;

        return date.toLocaleDateString('en-US', {
            month: 'short',
            day: 'numeric',
            hour: '2-digit',
            minute: '2-digit'
        });
    }

    /**
     * Format full timestamp for details view
     */
    function formatFullTimestamp(isoString) {
        const date = new Date(isoString);
        return date.toLocaleString('en-US', {
            year: 'numeric',
            month: 'short',
            day: 'numeric',
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit',
            hour12: true
        });
    }

    /**
     * Escape HTML to prevent XSS
     */
    function escapeHtml(str) {
        if (typeof str !== 'string') return str;
        const div = document.createElement('div');
        div.textContent = str;
        return div.innerHTML;
    }

    /**
     * Generate unique ID
     */
    function generateId() {
        return `evt_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
    }

    // ==========================================================================
    // Event Manager Class
    // ==========================================================================

    class EventManager {
        constructor() {
            this.events = [];
            this.filteredEvents = [];
            this.currentPage = 1;
            this.totalPages = 1;
            this.filters = {
                category: 'all',
                severity: 'all',
                search: '',
                timeRange: '24h'
            };
            this.stats = {
                total: 0,
                errors: 0,
                warnings: 0,
                success: 0
            };
            this.newEventsBuffer = [];
            this.isScrolledToTop = true;
        }

        /**
         * Add a new event to the manager
         */
        addEvent(event) {
            // Normalize event structure
            const normalizedEvent = {
                id: event.id || generateId(),
                timestamp: event.timestamp || new Date().toISOString(),
                severity: event.severity || 'info',
                category: event.category || 'system',
                title: event.title || 'Unknown Event',
                details: event.details || null,
                metadata: event.metadata || {}
            };

            // Add to buffer if user has scrolled down
            if (!this.isScrolledToTop) {
                this.newEventsBuffer.push(normalizedEvent);
                return normalizedEvent;
            }

            // Add to beginning of events array
            this.events.unshift(normalizedEvent);
            this.applyFilters();
            return normalizedEvent;
        }

        /**
         * Set events (initial load or refresh)
         */
        setEvents(events) {
            this.events = events.map(e => ({
                id: e.id || generateId(),
                timestamp: e.timestamp || new Date().toISOString(),
                severity: e.severity || 'info',
                category: e.category || 'system',
                title: e.title || 'Unknown Event',
                details: e.details || null,
                metadata: e.metadata || {}
            }));
            this.applyFilters();
        }

        /**
         * Apply current filters to events
         */
        applyFilters() {
            let filtered = [...this.events];

            // Category filter
            if (this.filters.category !== 'all') {
                filtered = filtered.filter(e => e.category === this.filters.category);
            }

            // Severity filter
            if (this.filters.severity !== 'all') {
                filtered = filtered.filter(e => e.severity === this.filters.severity);
            }

            // Search filter
            if (this.filters.search) {
                const searchLower = this.filters.search.toLowerCase();
                filtered = filtered.filter(e =>
                    e.title.toLowerCase().includes(searchLower) ||
                    (e.details && e.details.toLowerCase().includes(searchLower)) ||
                    e.category.toLowerCase().includes(searchLower)
                );
            }

            // Time range filter
            if (this.filters.timeRange !== 'all') {
                const now = new Date();
                let cutoff;
                switch (this.filters.timeRange) {
                    case '1h': cutoff = new Date(now - 3600000); break;
                    case '6h': cutoff = new Date(now - 21600000); break;
                    case '24h': cutoff = new Date(now - 86400000); break;
                    case '7d': cutoff = new Date(now - 604800000); break;
                    case '30d': cutoff = new Date(now - 2592000000); break;
                    default: cutoff = null;
                }
                if (cutoff) {
                    filtered = filtered.filter(e => new Date(e.timestamp) >= cutoff);
                }
            }

            this.filteredEvents = filtered;
            this.totalPages = Math.ceil(filtered.length / CONFIG.PAGE_SIZE);
            if (this.currentPage > this.totalPages) {
                this.currentPage = Math.max(1, this.totalPages);
            }
        }

        /**
         * Get events for current page
         */
        getCurrentPageEvents() {
            const start = (this.currentPage - 1) * CONFIG.PAGE_SIZE;
            const end = start + CONFIG.PAGE_SIZE;
            return this.filteredEvents.slice(start, end);
        }

        /**
         * Flush new events buffer
         */
        flushNewEvents() {
            if (this.newEventsBuffer.length > 0) {
                this.events = [...this.newEventsBuffer, ...this.events];
                this.newEventsBuffer = [];
                this.applyFilters();
            }
        }

        /**
         * Update statistics
         */
        updateStats(stats) {
            this.stats = { ...this.stats, ...stats };
        }

        /**
         * Set filter value
         */
        setFilter(key, value) {
            this.filters[key] = value;
            this.currentPage = 1;
            this.applyFilters();
        }
    }

    // ==========================================================================
    // SSE Connection Manager
    // ==========================================================================

    class SSEManager {
        constructor(endpoint, handlers) {
            this.endpoint = endpoint;
            this.handlers = handlers;
            this.eventSource = null;
            this.reconnectAttempts = 0;
            this.reconnectTimeout = null;
            this.isConnected = false;
        }

        /**
         * Connect to SSE endpoint
         */
        connect() {
            if (this.eventSource) {
                this.disconnect();
            }

            try {
                this.eventSource = new EventSource(this.endpoint);

                this.eventSource.onopen = () => {
                    this.isConnected = true;
                    this.reconnectAttempts = 0;
                    if (this.handlers.onConnect) {
                        this.handlers.onConnect();
                    }
                };

                this.eventSource.onmessage = (e) => {
                    try {
                        const data = JSON.parse(e.data);
                        if (this.handlers.onEvent) {
                            this.handlers.onEvent(data);
                        }
                    } catch (err) {
                        console.error('Failed to parse SSE message:', err);
                    }
                };

                this.eventSource.onerror = () => {
                    this.isConnected = false;
                    if (this.handlers.onDisconnect) {
                        this.handlers.onDisconnect();
                    }
                    this.scheduleReconnect();
                };

                // Listen for specific event types
                this.eventSource.addEventListener('event', (e) => {
                    try {
                        const data = JSON.parse(e.data);
                        if (this.handlers.onEvent) {
                            this.handlers.onEvent(data);
                        }
                    } catch (err) {
                        console.error('Failed to parse event:', err);
                    }
                });

                this.eventSource.addEventListener('stats', (e) => {
                    try {
                        const data = JSON.parse(e.data);
                        if (this.handlers.onStats) {
                            this.handlers.onStats(data);
                        }
                    } catch (err) {
                        console.error('Failed to parse stats:', err);
                    }
                });

            } catch (err) {
                console.error('Failed to create EventSource:', err);
                this.scheduleReconnect();
            }
        }

        /**
         * Disconnect from SSE
         */
        disconnect() {
            if (this.eventSource) {
                this.eventSource.close();
                this.eventSource = null;
            }
            this.isConnected = false;
            clearTimeout(this.reconnectTimeout);
        }

        /**
         * Schedule reconnection attempt
         */
        scheduleReconnect() {
            if (this.reconnectAttempts >= CONFIG.MAX_RECONNECT_ATTEMPTS) {
                if (this.handlers.onMaxReconnects) {
                    this.handlers.onMaxReconnects();
                }
                return;
            }

            this.reconnectAttempts++;
            if (this.handlers.onReconnecting) {
                this.handlers.onReconnecting(this.reconnectAttempts);
            }

            this.reconnectTimeout = setTimeout(() => {
                this.connect();
            }, CONFIG.RECONNECT_DELAY * Math.min(this.reconnectAttempts, 5));
        }
    }

    // ==========================================================================
    // UI Controller
    // ==========================================================================

    class UIController {
        constructor(eventManager) {
            this.eventManager = eventManager;
            this.elements = {};
            this.expandedEvents = new Set();
            this.initElements();
            this.bindEvents();
        }

        /**
         * Cache DOM elements
         */
        initElements() {
            this.elements = {
                // Header
                connectionStatus: document.getElementById('connectionStatus'),
                exportLogsBtn: document.getElementById('exportLogsBtn'),

                // Stats
                totalCount: document.getElementById('totalCount'),
                errorCount: document.getElementById('errorCount'),
                warningCount: document.getElementById('warningCount'),
                successCount: document.getElementById('successCount'),

                // Filters
                searchInput: document.getElementById('searchInput'),
                searchClear: document.getElementById('searchClear'),
                categoryFilters: document.getElementById('categoryFilters'),
                severityFilters: document.getElementById('severityFilters'),
                timeRange: document.getElementById('timeRange'),
                autoRefresh: document.getElementById('autoRefresh'),

                // Events
                eventsCount: document.getElementById('eventsCount'),
                refreshBtn: document.getElementById('refreshBtn'),
                eventsContainer: document.getElementById('eventsContainer'),
                eventsList: document.getElementById('eventsList'),
                newEventsIndicator: document.getElementById('newEventsIndicator'),
                newEventsCount: document.getElementById('newEventsCount'),
                loadingIndicator: document.getElementById('loadingIndicator'),
                emptyState: document.getElementById('emptyState'),

                // Pagination
                pagination: document.getElementById('pagination'),
                prevPage: document.getElementById('prevPage'),
                nextPage: document.getElementById('nextPage'),
                currentPage: document.getElementById('currentPage'),
                totalPages: document.getElementById('totalPages'),

                // Modal
                eventModal: document.getElementById('eventModal'),
                modalClose: document.getElementById('modalClose'),
                modalBody: document.getElementById('modalBody'),

                // Toast
                toastContainer: document.getElementById('toastContainer')
            };
        }

        /**
         * Bind event handlers
         */
        bindEvents() {
            // Search
            this.elements.searchInput.addEventListener('input', debounce((e) => {
                const value = e.target.value.trim();
                this.eventManager.setFilter('search', value);
                this.elements.searchClear.hidden = !value;
                this.renderEvents();
            }, CONFIG.DEBOUNCE_DELAY));

            this.elements.searchClear.addEventListener('click', () => {
                this.elements.searchInput.value = '';
                this.elements.searchClear.hidden = true;
                this.eventManager.setFilter('search', '');
                this.renderEvents();
            });

            // Category filters
            this.elements.categoryFilters.addEventListener('click', (e) => {
                const chip = e.target.closest('.filter-chip');
                if (!chip) return;

                const category = chip.dataset.category;
                this.elements.categoryFilters.querySelectorAll('.filter-chip').forEach(c => {
                    c.classList.toggle('active', c === chip);
                });
                this.eventManager.setFilter('category', category);
                this.renderEvents();
            });

            // Severity filters
            this.elements.severityFilters.addEventListener('click', (e) => {
                const chip = e.target.closest('.filter-chip');
                if (!chip) return;

                const severity = chip.dataset.severity;
                this.elements.severityFilters.querySelectorAll('.filter-chip').forEach(c => {
                    c.classList.toggle('active', c === chip);
                });
                this.eventManager.setFilter('severity', severity);
                this.renderEvents();
            });

            // Time range
            this.elements.timeRange.addEventListener('change', (e) => {
                this.eventManager.setFilter('timeRange', e.target.value);
                this.renderEvents();
            });

            // Refresh
            this.elements.refreshBtn.addEventListener('click', () => {
                this.loadEvents();
            });

            // New events indicator
            this.elements.newEventsIndicator.addEventListener('click', () => {
                this.eventManager.flushNewEvents();
                this.eventManager.isScrolledToTop = true;
                this.elements.newEventsIndicator.hidden = true;
                this.renderEvents();
                this.elements.eventsContainer.scrollTo({ top: 0, behavior: 'smooth' });
            });

            // Scroll detection for new events
            this.elements.eventsContainer.addEventListener('scroll', debounce(() => {
                const scrollTop = this.elements.eventsContainer.scrollTop;
                this.eventManager.isScrolledToTop = scrollTop < 100;

                if (this.eventManager.isScrolledToTop &&
                    this.eventManager.newEventsBuffer.length > 0) {
                    this.eventManager.flushNewEvents();
                    this.elements.newEventsIndicator.hidden = true;
                    this.renderEvents();
                }
            }, 100));

            // Pagination
            this.elements.prevPage.addEventListener('click', () => {
                if (this.eventManager.currentPage > 1) {
                    this.eventManager.currentPage--;
                    this.renderEvents();
                }
            });

            this.elements.nextPage.addEventListener('click', () => {
                if (this.eventManager.currentPage < this.eventManager.totalPages) {
                    this.eventManager.currentPage++;
                    this.renderEvents();
                }
            });

            // Modal
            this.elements.modalClose.addEventListener('click', () => {
                this.closeModal();
            });

            this.elements.eventModal.addEventListener('click', (e) => {
                if (e.target === this.elements.eventModal) {
                    this.closeModal();
                }
            });

            document.addEventListener('keydown', (e) => {
                if (e.key === 'Escape' && !this.elements.eventModal.hidden) {
                    this.closeModal();
                }
            });

            // Export
            this.elements.exportLogsBtn.addEventListener('click', () => {
                this.exportLogs();
            });

            // Event list delegation
            this.elements.eventsList.addEventListener('click', (e) => {
                const eventEntry = e.target.closest('.event-entry');
                if (!eventEntry) return;

                const eventId = eventEntry.dataset.eventId;
                const expandBtn = e.target.closest('.event-expand');

                if (expandBtn || e.target.closest('.event-main')) {
                    this.toggleEventExpand(eventId, eventEntry);
                }
            });
        }

        /**
         * Update connection status display
         */
        setConnectionStatus(status) {
            const el = this.elements.connectionStatus;
            el.className = 'connection-status ' + status;

            const textEl = el.querySelector('.status-text');
            switch (status) {
                case 'connected':
                    textEl.textContent = 'Live';
                    break;
                case 'disconnected':
                    textEl.textContent = 'Disconnected';
                    break;
                case 'reconnecting':
                    textEl.textContent = 'Reconnecting...';
                    break;
                default:
                    textEl.textContent = 'Connecting...';
            }
        }

        /**
         * Update stats display
         */
        updateStats(stats) {
            this.elements.totalCount.textContent = this.formatNumber(stats.total || 0);
            this.elements.errorCount.textContent = this.formatNumber(stats.errors || 0);
            this.elements.warningCount.textContent = this.formatNumber(stats.warnings || 0);
            this.elements.successCount.textContent = this.formatNumber(stats.success || 0);
        }

        /**
         * Format number for display
         */
        formatNumber(num) {
            if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M';
            if (num >= 1000) return (num / 1000).toFixed(1) + 'K';
            return num.toString();
        }

        /**
         * Render events list
         */
        renderEvents() {
            const events = this.eventManager.getCurrentPageEvents();

            // Update count
            this.elements.eventsCount.textContent =
                `${this.eventManager.filteredEvents.length} event${this.eventManager.filteredEvents.length !== 1 ? 's' : ''}`;

            // Show/hide states
            this.elements.loadingIndicator.hidden = true;
            this.elements.emptyState.hidden = events.length > 0;
            this.elements.eventsList.hidden = events.length === 0;

            if (events.length === 0) {
                this.elements.pagination.hidden = true;
                return;
            }

            // Render events
            this.elements.eventsList.innerHTML = events.map(event =>
                this.renderEventEntry(event)
            ).join('');

            // Update pagination
            this.updatePagination();

            // Update new events indicator
            if (this.eventManager.newEventsBuffer.length > 0) {
                this.elements.newEventsCount.textContent =
                    this.eventManager.newEventsBuffer.length;
                this.elements.newEventsIndicator.hidden = false;
            }
        }

        /**
         * Render single event entry
         */
        renderEventEntry(event) {
            const isExpanded = this.expandedEvents.has(event.id);
            const hasDetails = event.details || (event.metadata && Object.keys(event.metadata).length > 0);

            return `
                <article class="event-entry severity-${event.severity} ${isExpanded ? 'expanded' : ''}"
                         data-event-id="${event.id}">
                    <div class="event-main">
                        <div class="event-severity-icon ${event.severity}">
                            ${SEVERITY_ICONS[event.severity] || SEVERITY_ICONS.info}
                        </div>
                        <div class="event-content">
                            <h3 class="event-title">${escapeHtml(event.title)}</h3>
                            <div class="event-meta">
                                <span class="event-category ${event.category}">${event.category}</span>
                                <span class="event-timestamp" title="${formatFullTimestamp(event.timestamp)}">
                                    ${formatTimestamp(event.timestamp)}
                                </span>
                            </div>
                        </div>
                        ${hasDetails ? `
                            <button class="event-expand" aria-label="Toggle details">
                                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                                    <path d="M19 9l-7 7-7-7"/>
                                </svg>
                            </button>
                        ` : ''}
                    </div>
                    ${hasDetails ? `
                        <div class="event-details">
                            <div class="event-details-content">
                                ${this.renderEventDetails(event)}
                            </div>
                        </div>
                    ` : ''}
                </article>
            `;
        }

        /**
         * Render event details section
         */
        renderEventDetails(event) {
            let html = '';

            if (event.details) {
                html += `<div class="detail-row">
                    <span class="detail-key">Details</span>
                    <span class="detail-value">${escapeHtml(event.details)}</span>
                </div>`;
            }

            if (event.metadata && Object.keys(event.metadata).length > 0) {
                for (const [key, value] of Object.entries(event.metadata)) {
                    const displayValue = typeof value === 'object'
                        ? JSON.stringify(value, null, 2)
                        : String(value);
                    html += `<div class="detail-row">
                        <span class="detail-key">${escapeHtml(key)}</span>
                        <span class="detail-value">${escapeHtml(displayValue)}</span>
                    </div>`;
                }
            }

            html += `<div class="detail-row">
                <span class="detail-key">Event ID</span>
                <span class="detail-value">${escapeHtml(event.id)}</span>
            </div>`;

            html += `<div class="detail-row">
                <span class="detail-key">Full Timestamp</span>
                <span class="detail-value">${formatFullTimestamp(event.timestamp)}</span>
            </div>`;

            return html;
        }

        /**
         * Toggle event expansion
         */
        toggleEventExpand(eventId, element) {
            if (this.expandedEvents.has(eventId)) {
                this.expandedEvents.delete(eventId);
                element.classList.remove('expanded');
            } else {
                this.expandedEvents.add(eventId);
                element.classList.add('expanded');
            }
        }

        /**
         * Update pagination controls
         */
        updatePagination() {
            const { currentPage, totalPages } = this.eventManager;

            this.elements.pagination.hidden = totalPages <= 1;
            this.elements.currentPage.textContent = currentPage;
            this.elements.totalPages.textContent = totalPages;
            this.elements.prevPage.disabled = currentPage <= 1;
            this.elements.nextPage.disabled = currentPage >= totalPages;
        }

        /**
         * Show loading state
         */
        showLoading() {
            this.elements.loadingIndicator.hidden = false;
            this.elements.eventsList.hidden = true;
            this.elements.emptyState.hidden = true;
        }

        /**
         * Open event detail modal
         */
        openModal(event) {
            this.elements.modalBody.innerHTML = this.renderEventDetails(event);
            this.elements.eventModal.hidden = false;
            document.body.style.overflow = 'hidden';
        }

        /**
         * Close event detail modal
         */
        closeModal() {
            this.elements.eventModal.hidden = true;
            document.body.style.overflow = '';
        }

        /**
         * Show toast notification
         */
        showToast(message, type = 'info') {
            const toast = document.createElement('div');
            toast.className = `toast ${type}`;
            toast.innerHTML = `
                <svg class="toast-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    ${type === 'error'
                        ? '<path d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"/>'
                        : '<path d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z"/>'}
                </svg>
                <span>${escapeHtml(message)}</span>
            `;

            this.elements.toastContainer.appendChild(toast);

            setTimeout(() => {
                toast.classList.add('hiding');
                setTimeout(() => toast.remove(), 300);
            }, CONFIG.TOAST_DURATION);
        }

        /**
         * Export logs to file
         */
        exportLogs() {
            const events = this.eventManager.filteredEvents;
            const csv = this.eventsToCSV(events);
            const blob = new Blob([csv], { type: 'text/csv' });
            const url = URL.createObjectURL(blob);

            const a = document.createElement('a');
            a.href = url;
            a.download = `xgrabba-events-${new Date().toISOString().split('T')[0]}.csv`;
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(url);

            this.showToast(`Exported ${events.length} events`, 'success');
        }

        /**
         * Convert events to CSV format
         */
        eventsToCSV(events) {
            const headers = ['Timestamp', 'Severity', 'Category', 'Title', 'Details'];
            const rows = events.map(e => [
                e.timestamp,
                e.severity,
                e.category,
                `"${(e.title || '').replace(/"/g, '""')}"`,
                `"${(e.details || '').replace(/"/g, '""')}"`
            ]);

            return [headers.join(','), ...rows.map(r => r.join(','))].join('\n');
        }

        /**
         * Load events from API
         */
        async loadEvents() {
            this.showLoading();

            try {
                const params = new URLSearchParams({
                    range: this.eventManager.filters.timeRange,
                    limit: '500'
                });

                const response = await fetch(`${CONFIG.EVENTS_ENDPOINT}?${params}`);
                if (!response.ok) throw new Error('Failed to load events');

                const data = await response.json();
                this.eventManager.setEvents(data.events || []);
                this.renderEvents();

                if (data.stats) {
                    this.eventManager.updateStats(data.stats);
                    this.updateStats(data.stats);
                }
            } catch (err) {
                console.error('Failed to load events:', err);
                this.showToast('Failed to load events', 'error');
                this.elements.loadingIndicator.hidden = true;
                this.elements.emptyState.hidden = false;
            }
        }

        /**
         * Load stats from API
         */
        async loadStats() {
            try {
                const response = await fetch(CONFIG.STATS_ENDPOINT);
                if (!response.ok) throw new Error('Failed to load stats');

                const stats = await response.json();
                this.eventManager.updateStats(stats);
                this.updateStats(stats);
            } catch (err) {
                console.error('Failed to load stats:', err);
            }
        }
    }

    // ==========================================================================
    // Application Initialization
    // ==========================================================================

    class App {
        constructor() {
            this.eventManager = new EventManager();
            this.ui = new UIController(this.eventManager);
            this.sse = null;
            this.init();
        }

        init() {
            // Load initial data
            this.ui.loadEvents();
            this.ui.loadStats();

            // Initialize SSE if auto-refresh is enabled
            if (this.ui.elements.autoRefresh.checked) {
                this.connectSSE();
            }

            // Handle auto-refresh toggle
            this.ui.elements.autoRefresh.addEventListener('change', (e) => {
                if (e.target.checked) {
                    this.connectSSE();
                } else {
                    this.disconnectSSE();
                }
            });

            // Periodic stats refresh (backup for SSE)
            setInterval(() => {
                if (!this.sse || !this.sse.isConnected) {
                    this.ui.loadStats();
                }
            }, 30000);
        }

        connectSSE() {
            this.sse = new SSEManager(CONFIG.SSE_ENDPOINT, {
                onConnect: () => {
                    this.ui.setConnectionStatus('connected');
                },
                onDisconnect: () => {
                    this.ui.setConnectionStatus('disconnected');
                },
                onReconnecting: (attempt) => {
                    this.ui.setConnectionStatus('reconnecting');
                },
                onMaxReconnects: () => {
                    this.ui.setConnectionStatus('disconnected');
                    this.ui.showToast('Lost connection to server', 'error');
                },
                onEvent: (event) => {
                    this.eventManager.addEvent(event);

                    // If scrolled to top, render immediately
                    if (this.eventManager.isScrolledToTop) {
                        this.ui.renderEvents();
                    } else {
                        // Update new events indicator
                        this.ui.elements.newEventsCount.textContent =
                            this.eventManager.newEventsBuffer.length;
                        this.ui.elements.newEventsIndicator.hidden = false;
                    }
                },
                onStats: (stats) => {
                    this.eventManager.updateStats(stats);
                    this.ui.updateStats(stats);
                }
            });

            this.sse.connect();
        }

        disconnectSSE() {
            if (this.sse) {
                this.sse.disconnect();
                this.sse = null;
            }
            this.ui.setConnectionStatus('disconnected');
        }
    }

    // ==========================================================================
    // Demo Mode (for testing without backend)
    // ==========================================================================

    function runDemoMode() {
        console.log('Running in demo mode - simulating events');

        const app = window.xgrabbaApp;

        // Sample events for demo
        const sampleEvents = [
            {
                id: 'evt_001',
                timestamp: new Date(Date.now() - 60000).toISOString(),
                severity: 'success',
                category: 'export',
                title: 'USB export completed successfully',
                details: 'Exported 1,247 tweets to encrypted archive',
                metadata: {
                    device: 'SanDisk Ultra 64GB',
                    size: '2.3 GB',
                    encryption: 'AES-256'
                }
            },
            {
                id: 'evt_002',
                timestamp: new Date(Date.now() - 120000).toISOString(),
                severity: 'info',
                category: 'bookmark',
                title: 'Bookmark sync started',
                details: 'Checking for new bookmarks from X/Twitter'
            },
            {
                id: 'evt_003',
                timestamp: new Date(Date.now() - 180000).toISOString(),
                severity: 'warning',
                category: 'system',
                title: 'Disk space running low',
                details: 'Less than 10GB remaining on primary storage',
                metadata: {
                    available: '8.2 GB',
                    total: '256 GB',
                    threshold: '10 GB'
                }
            },
            {
                id: 'evt_004',
                timestamp: new Date(Date.now() - 300000).toISOString(),
                severity: 'error',
                category: 'ai',
                title: 'Whisper transcription failed',
                details: 'Audio file could not be processed',
                metadata: {
                    file: 'video_12345.mp4',
                    error: 'CUDA out of memory',
                    duration: '45:23'
                }
            },
            {
                id: 'evt_005',
                timestamp: new Date(Date.now() - 600000).toISOString(),
                severity: 'success',
                category: 'ai',
                title: 'Grok naming completed',
                details: 'Generated descriptive names for 52 tweets'
            },
            {
                id: 'evt_006',
                timestamp: new Date(Date.now() - 900000).toISOString(),
                severity: 'info',
                category: 'usb',
                title: 'USB drive detected',
                details: 'New storage device connected',
                metadata: {
                    device: '/dev/sdb1',
                    label: 'BACKUP_DRIVE',
                    filesystem: 'exFAT',
                    capacity: '128 GB'
                }
            },
            {
                id: 'evt_007',
                timestamp: new Date(Date.now() - 1800000).toISOString(),
                severity: 'success',
                category: 'archive',
                title: 'Tweet archived successfully',
                details: 'Archived tweet with media attachments',
                metadata: {
                    tweet_id: '1234567890',
                    author: '@example_user',
                    media_count: 3
                }
            },
            {
                id: 'evt_008',
                timestamp: new Date(Date.now() - 3600000).toISOString(),
                severity: 'error',
                category: 'bookmark',
                title: 'Authentication failed',
                details: 'Could not authenticate with X/Twitter API',
                metadata: {
                    error_code: 401,
                    message: 'Invalid or expired token'
                }
            }
        ];

        // Set initial events
        app.eventManager.setEvents(sampleEvents);
        app.ui.renderEvents();

        // Update stats
        const stats = {
            total: 1547,
            errors: 12,
            warnings: 34,
            success: 1423
        };
        app.eventManager.updateStats(stats);
        app.ui.updateStats(stats);
        app.ui.setConnectionStatus('connected');

        // Simulate real-time events
        const eventTypes = [
            { severity: 'info', category: 'bookmark', title: 'New bookmark detected' },
            { severity: 'success', category: 'archive', title: 'Tweet archived successfully' },
            { severity: 'info', category: 'ai', title: 'AI processing started' },
            { severity: 'success', category: 'export', title: 'Export batch completed' },
            { severity: 'warning', category: 'system', title: 'High CPU usage detected' },
            { severity: 'info', category: 'usb', title: 'USB drive activity' }
        ];

        setInterval(() => {
            if (!app.ui.elements.autoRefresh.checked) return;

            const template = eventTypes[Math.floor(Math.random() * eventTypes.length)];
            const newEvent = {
                ...template,
                id: generateId(),
                timestamp: new Date().toISOString(),
                details: `Simulated event at ${new Date().toLocaleTimeString()}`
            };

            app.eventManager.addEvent(newEvent);

            if (app.eventManager.isScrolledToTop) {
                app.ui.renderEvents();
            } else {
                app.ui.elements.newEventsCount.textContent =
                    app.eventManager.newEventsBuffer.length;
                app.ui.elements.newEventsIndicator.hidden = false;
            }
        }, 5000);
    }

    // ==========================================================================
    // Start Application
    // ==========================================================================

    document.addEventListener('DOMContentLoaded', () => {
        window.xgrabbaApp = new App();

        // Check if we should run in demo mode (no backend available)
        // Uncomment the line below for standalone demo
        // runDemoMode();

        // Or auto-detect by checking if API is available
        fetch(CONFIG.EVENTS_ENDPOINT)
            .then(response => {
                if (!response.ok) throw new Error('API not available');
            })
            .catch(() => {
                console.log('API not available, switching to demo mode');
                runDemoMode();
            });
    });

})();
