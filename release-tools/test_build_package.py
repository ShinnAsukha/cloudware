import hashlib
import json
import pathlib
import tempfile
import unittest
from build_package import build, validate

TAG='cw-app-0.5.6'
class PackageTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base=pathlib.Path(self.temp.name)
        self.candidate=self.base/TAG
        name='apps/web/app/update-center-test.tsx'
        content=b'export const updated=true;\n'
        file=self.candidate/'payload'/name
        file.parent.mkdir(parents=True)
        file.write_bytes(content)
        self.manifest={'schema':1,'tag':TAG,'database_changes':False,'services':['web'],
           'files':[{'path':name,'before_sha256':'absent','sha256':hashlib.sha256(content).hexdigest()}]}
        self.save()
    def save(self):
        (self.candidate/'manifest.json').write_text(json.dumps(self.manifest),encoding='utf-8')
    def test_valid_package(self):
        build(self.candidate,TAG,self.base/'out.zip')
        self.assertTrue((self.base/'out.zip').is_file())
    def test_bad_hash(self):
        self.manifest['files'][0]['sha256']='0'*64;self.save()
        with self.assertRaises(ValueError):validate(self.candidate,TAG)
    def test_bad_path(self):
        self.manifest['files'][0]['path']='apps/web/../../evil';self.save()
        with self.assertRaises(ValueError):validate(self.candidate,TAG)
    def test_database_change(self):
        self.manifest['database_changes']=True;self.save()
        with self.assertRaises(ValueError):validate(self.candidate,TAG)
    def test_extra_file(self):
        (self.candidate/'payload'/'AI').mkdir(parents=True)
        (self.candidate/'payload'/'AI'/'secret.txt').write_text('secret')
        with self.assertRaises(ValueError):validate(self.candidate,TAG)
    def test_duplicate_file(self):
        self.manifest['files'].append(self.manifest['files'][0]);self.save()
        with self.assertRaises(ValueError):validate(self.candidate,TAG)
    def test_missing_service(self):
        self.manifest['services']=[];self.save()
        with self.assertRaises(ValueError):validate(self.candidate,TAG)
    def test_missing_file(self):
        (self.candidate/'payload'/'apps/web/app/update-center-test.tsx').unlink()
        with self.assertRaises(ValueError):validate(self.candidate,TAG)
    def test_wrong_tag(self):
        with self.assertRaises(ValueError):validate(self.candidate,'cw-app-0.5.7')
if __name__=='__main__':unittest.main()