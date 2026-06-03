// Minimal @angular/core shim so the fixture type-checks without the real
// package. Only the Component decorator shape the indexer reads is needed.
export interface ComponentMetadata {
  selector?: string;
  template?: string;
  templateUrl?: string;
}
export function Component(_meta: ComponentMetadata): ClassDecorator {
  return () => {};
}
