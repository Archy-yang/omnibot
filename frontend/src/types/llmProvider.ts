export type LLMProviderMode = 'openai_compatible' | 'native';
export type LLMProviderStatus = 'available' | 'disabled';

export interface LLMProviderOption {
  value: string;
  label: string;
  mode: LLMProviderMode;
  status: LLMProviderStatus;
  default_base_url: string;
  default_model: string;
  description: string;
  disabled_reason: string;
}

export interface GetLLMProvidersResponse {
  providers: LLMProviderOption[];
}
