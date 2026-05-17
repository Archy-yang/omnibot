// OmniBot Web Chat Application

class ChatApp {
    constructor() {
        this.sessionId = this.getOrCreateSessionId();
        this.isLoading = false;
        this.loadingMessageId = null;

        this.initElements();
        this.bindEvents();
        this.loadHistory();
    }

    // Get or create session ID stored in localStorage
    getOrCreateSessionId() {
        let sessionId = localStorage.getItem('omnibot_session_id');
        if (!sessionId) {
            sessionId = 'web_' + this.generateUUID();
            localStorage.setItem('omnibot_session_id', sessionId);
        }
        return sessionId;
    }

    // Generate UUID v4
    generateUUID() {
        return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function(c) {
            const r = Math.random() * 16 | 0;
            const v = c === 'x' ? r : (r & 0x3 | 0x8);
            return v.toString(16);
        });
    }

    // Initialize DOM elements
    initElements() {
        this.messagesContainer = document.getElementById('chat-messages');
        this.form = document.getElementById('chat-form');
        this.input = document.getElementById('message-input');
        this.sendButton = document.getElementById('send-button');
    }

    // Bind event listeners
    bindEvents() {
        this.form.addEventListener('submit', (e) => this.handleSubmit(e));
        this.input.addEventListener('keydown', (e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                this.form.dispatchEvent(new Event('submit'));
            }
        });
    }

    // Handle form submission
    async handleSubmit(e) {
        e.preventDefault();

        const content = this.input.value.trim();
        if (!content || this.isLoading) return;

        // Clear input immediately
        this.input.value = '';

        // Show user message
        this.addMessage('user', content);

        // Show loading state
        this.isLoading = true;
        this.sendButton.disabled = true;
        this.addLoadingMessage();

        try {
            const response = await fetch('/api/v1/chat/messages', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({
                    session_id: this.sessionId,
                    content: content,
                }),
            });

            if (!response.ok) {
                throw new Error('Network response was not ok');
            }

            const data = await response.json();

            // Remove loading and show actual response
            this.removeLoadingMessage();
            this.addMessage('assistant', data.data.content);

        } catch (error) {
            this.removeLoadingMessage();
            this.addMessage('assistant', '抱歉，发生了错误，请稍后重试。');
            console.error('Error sending message:', error);
        } finally {
            this.isLoading = false;
            this.sendButton.disabled = false;
            this.input.focus();
        }
    }

    // Load message history
    async loadHistory() {
        try {
            const response = await fetch(
                `/api/v1/chat/messages?session_id=${encodeURIComponent(this.sessionId)}`
            );

            if (!response.ok) {
                throw new Error('Failed to load history');
            }

            const data = await response.json();

            // Clear welcome message
            this.messagesContainer.innerHTML = '';

            // Add messages
            if (data.data.messages && data.data.messages.length > 0) {
                data.data.messages.forEach(msg => {
                    this.addMessage(msg.role, msg.content, false);
                });
            } else {
                // Show welcome message for new user
                this.addMessage('assistant', '你好！我是 OmniBot，有什么可以帮你的吗？', false);
            }

            this.scrollToBottom();

        } catch (error) {
            console.error('Error loading history:', error);
        }
    }

    // Add a message to the UI
    addMessage(role, content, scrollToBottom = true) {
        const messageEl = document.createElement('div');
        messageEl.className = `message ${role}`;
        messageEl.innerHTML = `
            <div class="message-content">${this.escapeHtml(content)}</div>
        `;
        this.messagesContainer.appendChild(messageEl);

        if (scrollToBottom) {
            this.scrollToBottom();
        }
    }

    // Add loading indicator
    addLoadingMessage() {
        const loadingEl = document.createElement('div');
        loadingEl.className = 'message assistant loading';
        loadingEl.id = 'loading-message';
        loadingEl.innerHTML = `
            <div class="message-content">思考中<span class="dots"></span></div>
        `;
        this.messagesContainer.appendChild(loadingEl);
        this.scrollToBottom();
    }

    // Remove loading indicator
    removeLoadingMessage() {
        const loadingEl = document.getElementById('loading-message');
        if (loadingEl) {
            loadingEl.remove();
        }
    }

    // Scroll to bottom of messages
    scrollToBottom() {
        this.messagesContainer.scrollTop = this.messagesContainer.scrollHeight;
    }

    // Escape HTML to prevent XSS
    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }
}

// Initialize app when DOM is ready
document.addEventListener('DOMContentLoaded', () => {
    new ChatApp();
});
