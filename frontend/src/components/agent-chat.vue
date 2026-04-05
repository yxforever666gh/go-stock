<template>
  <div class="agent-layout">
    <aside class="history-sidebar" :class="{ collapsed: sidebarCollapsed }">
      <div class="sidebar-brand">
        <div class="brand-avatar">AI</div>
        <div v-if="!sidebarCollapsed" class="brand-text">
          <div class="brand-title">AI 智能体</div>
          <div class="brand-subtitle">多轮分析与工具调用</div>
        </div>
      </div>

      <div class="sidebar-header">
        <t-button size="small" theme="primary" block @click="handleCreateSession" :disabled="isStreamLoad || !selectValue">
          {{ sidebarCollapsed ? '+' : '新建对话' }}
        </t-button>
        <t-button
          size="small"
          variant="text"
          class="sidebar-collapse-btn"
          @click="sidebarCollapsed = !sidebarCollapsed"
          :title="sidebarCollapsed ? '展开侧边栏' : '折叠侧边栏'"
        >
          {{ sidebarCollapsed ? '>' : '<' }}
        </t-button>
      </div>

      <div v-if="!sidebarCollapsed" class="session-list">
        <div
          v-for="item in sessionList"
          :key="item.sessionId"
          class="session-item"
          :class="{ active: item.sessionId === activeSessionId }"
          @click="switchSession(item.sessionId)"
        >
          <div class="session-main">
            <div class="session-title" :title="item.title">{{ item.title || '新对话' }}</div>
            <div class="session-meta">{{ formatSessionMeta(item) }}</div>
          </div>
          <t-button
            variant="text"
            size="small"
            class="session-delete"
            :disabled="isStreamLoad"
            @click.stop="removeSession(item.sessionId)"
          >删</t-button>
        </div>

        <div v-if="sessionList.length === 0" class="session-empty">
          还没有历史会话，点击上方按钮开始第一轮对话。
        </div>
      </div>
    </aside>

    <div v-if="turns.length > 0" class="turn-rail" aria-label="对话轮次导航">
      <div class="turn-rail-line" aria-hidden="true"></div>
      <button
        v-for="t in turns"
        :key="t.id"
        type="button"
        class="turn-rail-dot"
        :class="{ active: t.idx === activeTurnIdx }"
        :title="t.title"
        @click="jumpToTurn(t.idx)"
      ></button>
    </div>

    <main class="chat-box">
      <div class="chat-panel">
        <div class="chat-topbar">
          <div class="chat-topbar-title">{{ activeSessionTitle }}</div>
          <div class="chat-topbar-meta">
            <span class="status-pill" :class="isStreamLoad ? 'streaming' : ''">
              {{ isStreamLoad ? '生成中' : '就绪' }}
            </span>
          </div>
        </div>

        <t-chat
          ref="chatRef"
          class="agent-chat"
          :data="chatList"
          :text-loading="loading"
          :is-stream-load="isStreamLoad"
          :reverse="false"
          style="height: 100%"
          @scroll="handleChatScroll"
          @clear="clearConfirm"
        >
          <template #content="{ item }">
            <div class="msg-wrap" :data-msg-id="item.id" :data-role="item.role">
            <t-chat-reasoning
              v-if="item.role === 'assistant' && (item.streaming || item.reasoning?.length > 0)"
              expand-icon-placement="right"
            >
              <t-chat-loading v-if="item.streaming" text="思考中..." />
              <t-chat-content v-if="item.reasoning?.length > 0" :content="item.reasoning" />
            </t-chat-reasoning>
            <t-chat-content v-if="item.content?.length > 0" :content="item.content" />
            </div>
          </template>

          <template #actions="{ item }">
            <t-chat-action :content="item.content" :operation-btn="['copy']" @operation="handleOperation" />
          </template>

          <template #footer>
            <div class="sender-shell">
              <t-chat-sender
                ref="chatSenderRef"
                v-model="inputValue"
                class="chat-sender"
                :textarea-props="{ placeholder: '发送消息...', autosize: { minRows: 1, maxRows: 2 } }"
                :loading="loading"
                :stop-disabled="isStreamLoad"
                @send="inputEnter"
                @stop="onStop"
              >
                <template #suffix>
                  <t-button theme="primary" size="medium" class="btn-send" :disabled="!inputValue.trim()" @click="inputEnter">
                    发送
                  </t-button>
                </template>
              </t-chat-sender>
            </div>
          </template>
        </t-chat>
      </div>
    </main>
  </div>
</template>

<script setup lang="ts">
import { useAgentChat } from '../composables/useAgentChat';
import 'tdesign-vue-next/es/style/index.css';

const {
  activeSessionId,
  activeSessionTitle,
  activeTurnIdx,
  chatList,
  chatRef,
  chatSenderRef,
  clearConfirm,
  formatSessionMeta,
  handleChatScroll,
  handleCreateSession,
  handleOperation,
  inputEnter,
  inputValue,
  isStreamLoad,
  jumpToTurn,
  loading,
  onStop,
  removeSession,
  selectValue,
  sessionList,
  sidebarCollapsed,
  switchSession,
  turns,
} = useAgentChat();
</script>

<style lang="less">
.agent-layout {
  --agent-bg: #f2f5f8;
  --agent-card: #ffffff;
  --agent-border: #dce3eb;
  --agent-text: #202939;
  --agent-muted: #6f7c91;
  --agent-brand: #10a37f;
  --agent-brand-hover: #0e8f70;

  display: flex;
  gap: 14px;
  height: calc(100vh - 120px);
  margin: 8px 10px;
  padding: 10px;
  border-radius: 14px;
  background: radial-gradient(circle at top right, #dce9ff 0%, #f3f7fb 40%, #eef4f9 100%);
}

.history-sidebar {
  width: 300px;
  min-width: 300px;
  border: 1px solid var(--agent-border);
  border-radius: 12px;
  background: linear-gradient(180deg, #f8fbff 0%, #f4f8fc 100%);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-shadow: inset 0 1px 0 #fff;

	  &.collapsed {
	    width: 62px;
	    min-width: 62px;
	  }
	}

.turn-rail {
  width: 18px;
  min-width: 18px;
  height: 100%;
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 14px 0;
  box-sizing: border-box;
  user-select: none;
}

.turn-rail-line {
  position: absolute;
  top: 12px;
  bottom: 12px;
  left: 50%;
  width: 2px;
  transform: translateX(-1px);
  background: linear-gradient(
    180deg,
    rgba(16, 163, 127, 0) 0%,
    rgba(16, 163, 127, 0.35) 12%,
    rgba(16, 163, 127, 0.35) 88%,
    rgba(16, 163, 127, 0) 100%
  );
  border-radius: 2px;
}

.turn-rail-dot {
  width: 10px;
  height: 10px;
  border-radius: 999px;
  border: 1px solid rgba(22, 127, 100, 0.35);
  background: #ffffff;
  box-shadow: 0 1px 4px rgba(18, 33, 54, 0.08);
  cursor: pointer;
  margin: 8px 0;
  transition: transform 0.12s ease, background 0.12s ease, border-color 0.12s ease;
  position: relative;
  z-index: 1;
}

.turn-rail-dot:hover {
  transform: scale(1.12);
  border-color: rgba(16, 163, 127, 0.7);
}

.turn-rail-dot.active {
  background: #10a37f;
  border-color: #10a37f;
  transform: scale(1.18);
}

.sidebar-brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 12px 10px;
  border-bottom: 1px solid var(--agent-border);
}

.brand-avatar {
  width: 30px;
  height: 30px;
  border-radius: 9px;
  background: linear-gradient(135deg, #12755f 0%, #10a37f 100%);
  color: #fff;
  font-weight: 700;
  font-size: 13px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.brand-text {
  min-width: 0;
}

.brand-title {
  font-size: 14px;
  line-height: 20px;
  color: #1f2737;
  font-weight: 700;
}

.brand-subtitle {
  margin-top: 2px;
  font-size: 12px;
  color: var(--agent-muted);
}

.sidebar-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px;
  border-bottom: 1px solid var(--agent-border);
}

.sidebar-collapse-btn {
  min-width: 30px;
  padding: 0;
}

.session-list {
  flex: 1;
  overflow-y: auto;
  padding: 8px;
}

.session-item {
  display: flex;
  align-items: center;
  gap: 6px;
  border: 1px solid transparent;
  border-radius: 10px;
  padding: 10px;
  cursor: pointer;
  transition: all 0.16s ease;

  &:hover {
    background: #f1f6fc;
    border-color: #e4ebf3;
  }

  &.active {
    border-color: #caeadd;
    background: linear-gradient(180deg, #eefaf6 0%, #e8f7f2 100%);
    box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.8);
  }
}

.session-main {
  flex: 1;
  min-width: 0;
}

.session-title {
  font-size: 13px;
  line-height: 18px;
  color: var(--agent-text);
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.session-meta {
  margin-top: 4px;
  font-size: 12px;
  color: var(--agent-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.session-delete {
  flex: 0 0 auto;
  color: #8d9cb4;

  &:hover {
    color: #e15454;
  }
}

.session-empty {
  margin: 10px 4px;
  padding: 12px;
  border: 1px dashed #d9e1ec;
  border-radius: 10px;
  font-size: 12px;
  line-height: 18px;
  color: #7c8aa0;
  background: #f8fbff;
}

.chat-box {
  flex: 1;
  min-width: 0;
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: stretch;
  text-align: left;
}

.chat-panel {
  width: 100%;
  max-width: 100%;
  min-width: 0;
  flex: 1;
  border: 1px solid var(--agent-border);
  border-radius: 12px;
  background: var(--agent-card);
  overflow: hidden;
  box-shadow: 0 8px 24px rgba(30, 52, 84, 0.08);
  display: flex;
  flex-direction: column;
}

.chat-topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--agent-border);
  background: linear-gradient(180deg, #fbfdff 0%, #f6faff 100%);
}

.chat-topbar-title {
  min-width: 0;
  font-size: 15px;
  font-weight: 700;
  color: #1f2737;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chat-topbar-meta {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 8px;
}

.status-pill {
  height: 24px;
  padding: 0 10px;
  border-radius: 999px;
  border: 1px solid #dce4ee;
  background: #fff;
  display: inline-flex;
  align-items: center;
  font-size: 12px;
  color: #667990;
}

.status-pill.streaming {
  border-color: #b8ebdc;
  background: #eaf8f3;
  color: #167f64;
}

.agent-chat {
  flex: 1;
  min-height: 0;
}

.sender-shell {
  padding: 2px 10px 6px;
  border-top: 1px solid var(--agent-border);
  background: linear-gradient(180deg, #ffffff 0%, #f8fbff 100%);
}

.chat-sender {
  border: 1px solid #d9e2ec;
  border-radius: 10px;
  background: #ffffff;
  box-shadow: 0 1px 8px rgba(18, 33, 54, 0.05);

  :deep(.t-chat-sender) {
    padding: 0;
  }

  :deep(.t-chat-sender__textarea) {
    padding: 6px 8px;
  }

  :deep(.t-textarea) {
    margin-bottom: 0;
    padding: 0;
  }

  :deep(.t-textarea__inner) {
    font-size: 13px;
    line-height: 20px;
    color: #233044;
    padding: 0;
  }
}

.btn-send {
  min-width: 64px;
  border-radius: 8px;
  background: var(--agent-brand);
  border-color: var(--agent-brand);

  &:hover {
    background: var(--agent-brand-hover);
    border-color: var(--agent-brand-hover);
  }
}

:deep(.agent-chat .t-chat__list) {
  padding: 18px 18px 8px;
  width: 100%;
  max-width: none;
}

/* 强制正常顺序，避免 reverse 样式导致“新消息跑到上面/滚动异常”。 */
:deep(.agent-chat .t-chat__list--reverse) {
  flex-direction: column !important;
}

/* 取消组件默认的 800px 限制，去掉右侧大空白。 */
:deep(.agent-chat .t-chat__detail) {
  max-width: none !important;
  width: 100% !important;
}

.msg-wrap {
  width: 100%;
}

:deep(.agent-chat .t-chat__item) {
  margin-bottom: 16px;
}

:deep(.agent-chat .t-chat__message) {
  border-radius: 14px;
  border: 1px solid #e5eaf1;
  box-shadow: 0 2px 9px rgba(17, 32, 52, 0.04);
  max-width: calc(100% - 48px);
}

:deep(.agent-chat .t-chat__item--assistant .t-chat__message) {
  background: #f8fbff;
}

:deep(.agent-chat .t-chat__item--user .t-chat__message) {
  background: #eff6ff;
  border-color: #d7e6ff;
}

:deep(.agent-chat .t-chat__text) {
  color: #1e2a3c;
  line-height: 1.72;
}

:deep(.agent-chat .t-chat__text pre) {
  border-radius: 10px;
  background: #f0f4fa;
}

@media (max-width: 1120px) {
  .history-sidebar {
    width: 248px;
    min-width: 248px;
  }

  .chat-panel {
    max-width: 100%;
  }

}

@media (max-width: 860px) {
  .agent-layout {
    height: calc(100vh - 136px);
    gap: 10px;
    padding: 8px;
  }

  .history-sidebar {
    width: 70px;
    min-width: 70px;

    &:not(.collapsed) {
      width: 210px;
      min-width: 210px;
    }
  }

  .chat-topbar {
    padding: 10px 12px;
  }

  .chat-topbar-title {
    font-size: 14px;
  }

  .sender-shell {
    padding: 4px 6px 6px;
  }
}

@media (max-width: 640px) {
  .agent-layout {
    margin: 4px;
    padding: 6px;
    display: block;
  }

  .history-sidebar {
    width: 100%;
    min-width: 0;
    margin-bottom: 8px;

    &.collapsed {
      width: 100%;
      min-width: 0;
      max-height: 60px;
    }
  }

  .chat-box {
    height: calc(100vh - 260px);
  }

  .turn-rail {
    display: none;
  }

  .chat-topbar-meta {
    gap: 4px;
  }

  .model-pill,
  .status-pill {
    padding: 0 8px;
    font-size: 11px;
  }

}
</style>
