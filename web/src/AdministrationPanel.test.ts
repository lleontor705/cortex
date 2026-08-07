import { describe, expect, it } from "vitest";
import { Principal } from "./api";
import { isAdminPrincipal } from "./AdministrationPanel";

function principal(roles: string[]): Principal {
  return {
    id: "user-1",
    type: "user",
    org_id: "org-1",
    workspaces: ["workspace-1"],
    projects: ["*"],
    roles,
    scopes: [],
    classification_clearance: ["*"],
    auth_method: "api_key",
  };
}

describe("administration permissions", () => {
  it.each([[["owner"]], [["admin"]], [["member", "admin"]]])(
    "allows administrative role set %j",
    (roles: string[]) => {
      expect(isAdminPrincipal(principal(roles))).toBe(true);
    },
  );

  it.each([[[]], [["member"]], [["viewer"]], [["service-account"]]])(
    "keeps non-administrative role set %j read-only",
    (roles: string[]) => {
      expect(isAdminPrincipal(principal(roles))).toBe(false);
    },
  );
});
