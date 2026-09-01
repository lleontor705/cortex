import type {
  AgentAnswer,
  AgentConfidence,
  AgentMessage,
  AgentRetrieval,
  AgentSource,
} from "./api";

export const MAX_AGENT_TURNS = 6;

export type AgentChatMessage = AgentMessage & {
  sources?: AgentSource[];
  confidence?: AgentConfidence;
  retrieval?: AgentRetrieval;
};

export type AgentChatState = {
  projectId: string;
  activeRequestId: string;
  messages: AgentChatMessage[];
  lastQuestion: string;
  pendingQuestion: string;
  draftAnswer: string;
  sources: AgentSource[];
  confidence?: AgentConfidence;
  retrieval?: AgentRetrieval;
  status: "idle" | "loading" | "streaming" | "error";
  error: string;
};

export const initialAgentChatState: AgentChatState = {
  projectId: "",
  activeRequestId: "",
  messages: [],
  lastQuestion: "",
  pendingQuestion: "",
  draftAnswer: "",
  sources: [],
  status: "idle",
  error: "",
};

export type AgentChatAction =
  | { type: "select_project"; projectId: string }
  | { type: "sync_projects"; projectIds: string[] }
  | { type: "ask"; question: string; requestId: string }
  | { type: "retry"; requestId: string }
  | { type: "meta"; requestId: string; confidence?: AgentConfidence; retrieval: AgentRetrieval }
  | { type: "delta"; requestId: string; text: string }
  | { type: "citation"; requestId: string; source: AgentSource }
  | { type: "complete"; requestId: string; answer: AgentAnswer }
  | { type: "error"; requestId: string; message: string }
  | { type: "stop" }
  | { type: "new_conversation" }
  | { type: "logout" };

function cleared(projectId: string): AgentChatState {
  return { ...initialAgentChatState, projectId };
}

function trimToSixTurns(messages: AgentChatMessage[]): AgentChatMessage[] {
  return messages.slice(-(MAX_AGENT_TURNS * 2));
}

function isCurrentRequest(state: AgentChatState, requestId: string): boolean {
  return requestId.length > 0 && requestId === state.activeRequestId;
}

export function agentChatReducer(state: AgentChatState, action: AgentChatAction): AgentChatState {
  switch (action.type) {
    case "select_project":
      return action.projectId === state.projectId ? state : cleared(action.projectId);
    case "sync_projects":
      return !state.projectId || action.projectIds.includes(state.projectId)
        ? state
        : cleared("");
    case "ask": {
      const question = action.question.trim();
      if (!state.projectId || !question || !action.requestId || state.status === "loading" || state.status === "streaming") {
        return state;
      }
      return {
        ...state,
        activeRequestId: action.requestId,
        lastQuestion: question,
        pendingQuestion: question,
        draftAnswer: "",
        sources: [],
        confidence: undefined,
        retrieval: undefined,
        status: "loading",
        error: "",
      };
    }
    case "retry":
      if (!state.projectId || !state.lastQuestion || !action.requestId || state.status === "loading" || state.status === "streaming") {
        return state;
      }
      return {
        ...state,
        activeRequestId: action.requestId,
        pendingQuestion: state.lastQuestion,
        draftAnswer: "",
        sources: [],
        confidence: undefined,
        retrieval: undefined,
        status: "loading",
        error: "",
      };
    case "meta":
      return isCurrentRequest(state, action.requestId)
        ? { ...state, confidence: action.confidence, retrieval: action.retrieval, status: "streaming" }
        : state;
    case "delta":
      if (!isCurrentRequest(state, action.requestId) || !state.pendingQuestion) return state;
      return { ...state, draftAnswer: state.draftAnswer + action.text, status: "streaming" };
    case "citation":
      if (!isCurrentRequest(state, action.requestId)) return state;
      return state.sources.some((source) => source.handle === action.source.handle)
        ? state
        : { ...state, sources: [...state.sources, action.source] };
    case "complete": {
      if (!isCurrentRequest(state, action.requestId) || !state.pendingQuestion) return state;
      const committed: AgentChatMessage[] = [
        ...state.messages,
        { role: "user", content: state.pendingQuestion },
        {
          role: "assistant",
          content: action.answer.answer,
          sources: action.answer.sources,
          confidence: action.answer.confidence,
          retrieval: action.answer.retrieval,
        },
      ];
      return {
        ...state,
        activeRequestId: "",
        messages: trimToSixTurns(committed),
        pendingQuestion: "",
        draftAnswer: "",
        sources: action.answer.sources,
        confidence: action.answer.confidence,
        retrieval: action.answer.retrieval,
        status: "idle",
        error: "",
      };
    }
    case "error":
      return isCurrentRequest(state, action.requestId)
        ? { ...state, activeRequestId: "", pendingQuestion: "", draftAnswer: "", sources: [], status: "error", error: action.message }
        : state;
    case "stop":
      return {
        ...state,
        activeRequestId: "",
        pendingQuestion: "",
        draftAnswer: "",
        sources: [],
        confidence: undefined,
        retrieval: undefined,
        status: "idle",
        error: "",
      };
    case "new_conversation":
      return cleared(state.projectId);
    case "logout":
      return initialAgentChatState;
  }
}

export function historyForAgentRequest(state: AgentChatState): AgentMessage[] {
  return state.messages.map(({ role, content }) => ({ role, content }));
}
