import { test } from "node:test";
import assert from "node:assert/strict";
import { collectDroppedFiles } from "./drag-drop.js";

function makeFile(name, type = "text/plain") {
  return new File([`content of ${name}`], name, { type });
}

function fileEntry(file, options = {}) {
  return {
    isFile: true,
    isDirectory: false,
    name: file.name,
    file: (resolve, reject) => (options.fail ? reject(new Error("unreadable")) : resolve(file)),
  };
}

function directoryEntry(name, batches) {
  let cursor = 0;
  return {
    isFile: false,
    isDirectory: true,
    name,
    createReader() {
      return {
        readEntries(resolve) {
          resolve(batches[cursor] || []);
          cursor += 1;
        },
      };
    },
  };
}

const itemsWith = (entries) => entries.map((entry) => ({ webkitGetAsEntry: () => entry }));

test("falls back to dataTransfer.files when the entries API is unavailable", async () => {
  const plain = makeFile("plain.txt");
  const result = await collectDroppedFiles({ files: [plain], items: [] });
  assert.equal(result.length, 1);
  assert.equal(result[0], plain);
});

test("a dropped file keeps its bare name and no relative path", async () => {
  const file = makeFile("root.txt");
  const result = await collectDroppedFiles({ files: [], items: itemsWith([fileEntry(file)]) });
  assert.equal(result.length, 1);
  assert.equal(result[0], file);
});

test("a dropped folder carries the relative path on every nested file", async () => {
  const inner = makeFile("deep.txt");
  const folder = directoryEntry("assets", [[fileEntry(makeFile("a.txt")), directoryEntry("sub", [[fileEntry(inner)]])]]);
  const result = await collectDroppedFiles({ files: [], items: itemsWith([folder]) });
  assert.deepEqual(result.map((file) => file.webkitRelativePath), ["assets/a.txt", "assets/sub/deep.txt"]);
  assert.equal(result[1].name, "deep.txt");
});

test("large folders are drained across readEntries batches", async () => {
  const first = makeFile("1.txt");
  const second = makeFile("2.txt");
  const folder = directoryEntry("big", [[fileEntry(first)], [fileEntry(second)], []]);
  const result = await collectDroppedFiles({ files: [], items: itemsWith([folder]) });
  assert.deepEqual(result.map((file) => file.webkitRelativePath), ["big/1.txt", "big/2.txt"]);
});

test("unreadable files and unknown entries are skipped without failing the drop", async () => {
  const good = makeFile("good.txt");
  const broken = fileEntry(makeFile("broken.txt"), { fail: true });
  const unknown = { isFile: false, isDirectory: false, name: "weird" };
  const folder = directoryEntry("mixed", [[broken, unknown, fileEntry(good)]]);
  const result = await collectDroppedFiles({ files: [], items: itemsWith([folder]) });
  assert.equal(result.length, 1);
  assert.equal(result[0].webkitRelativePath, "mixed/good.txt");
});
