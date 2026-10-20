import unittest

from cleanup_images import reachable, tagged


class CleanupTests(unittest.TestCase):
    def test_preserves_nested_indexes_and_attestations(self):
        graph = {
            'root': {'manifests': [{'digest': 'nested'}, {'digest': 'attestation'}]},
            'nested': {'manifests': [{'digest': 'amd64'}, {'digest': 'arm64'}]},
            'amd64': {}, 'arm64': {}, 'attestation': {},
        }
        self.assertEqual(reachable(['root'], graph.__getitem__), set(graph))

    def test_missing_child_aborts(self):
        with self.assertRaises(KeyError):
            reachable(['root'], {'root': {'manifests': [{'digest': 'missing'}]}}.__getitem__)

    def test_all_tagged_roots_are_selected(self):
        versions = [{'name': name, 'metadata': {'container': {'tags': tags}}}
                    for name, tags in [('a', ['latest']), ('b', ['stable']), ('c', [])]]
        self.assertEqual(tagged(versions), {'a': ('latest',), 'b': ('stable',)})
