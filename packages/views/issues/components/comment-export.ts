/**
 * Opens the comment image export (M-125) for one comment.
 *
 * The menu item's whole job. The request goes to the app-level modal store
 * rather than to card state because the issue timeline is virtualized and this
 * card can be recycled while the capture is still running — see
 * `comment-export-host.tsx`, which renders the surface.
 */

import { useModalStore } from "@multica/core/modals";
import type { TimelineEntry } from "@multica/core/types";

export function exportCommentImage(entry: TimelineEntry): void {
  useModalStore.getState().open("comment-image-export", { entry });
}
