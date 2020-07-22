package lib

import (
	"encoding/json"
	"fmt"
	"github.com/umnmsi/SimplePuppetProvisioner/lib/certsign"
	"github.com/umnmsi/SimplePuppetProvisioner/lib/nodeconfig"
	"html"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var nodeconfig_path = `/nodeconfig`

type NodeConfigHandler struct {
	appConfig      *AppConfig
	log            *log.Logger
	certSigner     certsign.CertSignerInterface
	nodeClassifier nodeconfig.NodeClassifierInterface
	jsonMarshaller func(v interface{}) ([]byte, error)
	actionTimeout  time.Duration
}

func NewNodeConfigHandler(appConfig *AppConfig, certSigner certsign.CertSignerInterface, nodeClassifier nodeconfig.NodeClassifierInterface) *NodeConfigHandler {
	handler := NodeConfigHandler{
		appConfig:      appConfig,
		log:            appConfig.Log,
		certSigner:     certSigner,
		nodeClassifier: nodeClassifier,
		jsonMarshaller: json.Marshal,
		actionTimeout:  time.Duration(appConfig.NodeConfigTimeout) * time.Second,
	}
	appConfig.Log.Printf("NodeConfigHandler classification timeout: %v\n", handler.actionTimeout)
	return &handler
}

func (ctx NodeConfigHandler) marshalJSON(v interface{}) ([]byte, error) {
	json, err := ctx.jsonMarshaller(v)
	return json, err
}

func (ctx NodeConfigHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	allowed, err := regexp.Match(`^`+nodeconfig_path+`($)`, []byte(request.URL.Path))
	if !allowed {
		response.WriteHeader(http.StatusNotFound)
		return
	}

	// Verify Uid and Gecos headers are present - if not, auth did not occur
	uid := request.Header.Get("Uid")
	gecos := request.Header.Get("Gecos")
	if uid == "" || gecos == "" {
		fmt.Fprintf(response, `Missing Uid or Gecos header - authentication appears to have been skipped. Bailing.`)
		return
	}
	ctx.log.Printf("%p Found uid %s, gecos %s\n", request, uid, gecos)

	action, cert, message := "", "", ""

	switch request.Method {
	case http.MethodGet:
		q := request.URL.Query()
		ctx.log.Printf("%p Received nodeconfig GET request\n", request)
		if q["action"] != nil {
			action = q["action"][0]
		}
		switch action {
		case "":
			if q["message"] != nil {
				message = html.EscapeString(q["message"][0])
			}
		case "lookupNode":
			if q["node"] == nil {
				fmt.Fprintf(response, `{"Status":"ERROR","Message":"Missing 'node' argument for action 'lookupNode'"}`)
				return
			}
			node := q["node"][0]
			config, err := ctx.nodeClassifier.GetClassification(node, false)
			if err != nil {
				fmt.Fprintf(response, `{"Status":"ERROR","Message":"Failed to get config for node %s: %s"}`, node, err)
				return
			}
			json, err := ctx.marshalJSON(nodeconfig.NodeConfigResult{
				Action:      "classify",
				Success:     true,
				Message:     "",
				Node:        node,
				Environment: config.Environment,
				PrimaryRole: config.PrimaryRole,
			})
			if err != nil {
				fmt.Fprintf(response, `{"Status":"ERROR","Message":"Failed to encode JSON: %s"}`, err)
				return
			}
			response.Write(json)
			return
		case "getEnvironments":
			environments, err := ctx.marshalJSON(ctx.nodeClassifier.GetEnvironments())
			if err != nil {
				fmt.Fprintf(response, `{"Status":"ERROR","Message":"Failed to encode JSON: %s"}`, err)
				return
			}
			response.Write(environments)
			return
		case "getRoles":
			if q["environment"] == nil {
				fmt.Fprintf(response, `{"Status":"ERROR","Message":"Missing 'environment' argument for action 'getRoles'"}`)
				return
			}
			environment := q["environment"][0]
			roles, err := ctx.marshalJSON(ctx.nodeClassifier.GetRoles(environment))
			if err != nil {
				fmt.Fprintf(response, `{"Status":"ERROR","Message":"Failed to encode JSON: %s"}`, err)
				return
			}
			response.Write(roles)
			return
		default:
			message = fmt.Sprintf("Invalid action '%s'", html.EscapeString(action))
		}
	case http.MethodPost:
		request.ParseForm()
		ctx.log.Printf("%p Received nodeconfig POST request: %#v\n", request, request.Form)
		action = request.Form.Get("action")
		switch action {
		case "sign":
			cert = request.Form.Get("cert")
			if cert == "" {
				message = "Missing 'cert' argument for action 'sign'"
			} else {
				signingResultChan := ctx.certSigner.Sign(cert, false)
				select {
				case res := <-signingResultChan:
					if res.Success {
						message = res.Message
					} else {
						message = fmt.Sprintf("Signing failed for %s: %s", cert, res.Message)
					}
				case <-time.After(ctx.actionTimeout):
					message = fmt.Sprintf("Signing failed for %s: Timed out", cert)
				}
			}
		case "classify":
			fallthrough
		case "classifycsr":
			cert = request.Form.Get("cert")
			if cert == "" {
				message = fmt.Sprintf("Missing 'cert' argument for action '%s'", html.EscapeString(action))
			} else {
				environment := request.Form.Get("environment")
				primary_role := request.Form.Get("primary_role")
				classificationResultChan := ctx.nodeClassifier.Classify(cert, environment, primary_role, true, gecos, uid+`@umn.edu`)
				select {
				case res := <-classificationResultChan:
					message = res.Message
				case <-time.After(ctx.actionTimeout):
					message = fmt.Sprintf("Classification failed for %s: Timed out", cert)
				}
			}
		default:
			message = fmt.Sprintf("Invalid action '%s'", html.EscapeString(action))
		}
		http.Redirect(response, request, fmt.Sprintf(`%s?message=%s`, nodeconfig_path, message), http.StatusSeeOther)
		return
	default:
		message = fmt.Sprintf("Invalid method '%s'", html.EscapeString(request.Method))
	}

	response.WriteHeader(http.StatusOK)

	fmt.Fprintf(response, `
<html>
<head>
<style type="text/css">
body { font-family: arial; }
form { margin-block-end: 0; }
table, div, a { font-size: 10pt; }
#message { margin-bottom: 10px; }
a:hover { text-decoration: underline; }
a { color: blue; text-decoration: none; }
a:visited { color: blue; }
button { font-size: 10pt; white-space: nowrap; }
th { padding: 0 5px; text-align: left; }
td { padding: 0 5px; }
tr:hover { background-color: #F0F0F0; }
</style>
<script type="text/javascript">
function lookupNode(node) {
	document.getElementById('nodemessage').innerHTML = '';
	document.getElementById('nodeclassify').style.display = 'inline';
	document.getElementById('nodeclassify').disabled = true;
	document.getElementById('nodesubmit').style.display = 'none';
	r = new XMLHttpRequest();
	r.onreadystatechange = function() {
		if (this.readyState === XMLHttpRequest.DONE) {
			if (this.status === 200) {
				response = JSON.parse(this.responseText);
				if (response.Message != '')
					document.getElementById('nodemessage').innerHTML = response.Message;
				document.getElementById('nodeenvironment').innerHTML = response.Environment;
				document.getElementById('noderole').innerHTML = response.PrimaryRole;
				document.getElementById('nodeclassify').disabled = false;
				document.getElementById('nodetr').setAttribute('data-cert', node);
			} else {
				document.getElementById('nodemessage').innerHTML = 'Problem: ' + r.responseText;
			}
		}
	}
	r.open('GET', '?action=lookupNode&node='+node);
	r.send();
}
function getEnvironments(id) {
	r = new XMLHttpRequest();
	r.onreadystatechange = function() {
		if (this.readyState === XMLHttpRequest.DONE) {
			if (this.status === 200) {
				msg = document.getElementById(id+'message');
				new_msg = msg.cloneNode(false);
				msg.parentNode.replaceChild(new_msg, msg);
				response = JSON.parse(this.responseText);
				if (response.Message != '')
					new_msg.appendChild(document.createElement('div')).innerHTML = response.Message;
				env = document.getElementById(id+'environment');
				new_env = env.cloneNode(false);
				old_env = env.hasChildNodes() && 'value' in env.firstChild ? env.firstChild.value : env.textContent;
				select = new_env.appendChild(document.createElement("select"))
				select.name = 'environment';
				select.addEventListener('change', function() { getRoles(id, this.value); }.bind(select));
				select.appendChild(document.createElement("option"))
				for (i = 0; i < response.Environments.length; i++) {
					option = select.appendChild(document.createElement("option"))
					option.value = option.innerHTML = response.Environments[i];
					if (option.value === old_env) {
						option.selected = true;
						getRoles(id, old_env);
					}
				}
				env.parentNode.replaceChild(new_env, env);
				if (old_env != '' && select.value != old_env)
					new_msg.appendChild(document.createElement('div')).innerHTML = "Couldn't find environment '"+old_env+"'";
				edit = document.getElementById(id+'classify')
				edit.style.display = 'none';
				submit = document.getElementById(id+'submit')
				submit.style.display = 'inline';
			} else {
				document.getElementById(id+'message').innerHTML = 'Problem: ' + r.responseText;
			}
		}
	}
	r.open('GET', '?action=getEnvironments');
	r.send();
	return false;
}
function getRoles(id, environment) {
	if (environment === '') {
		document.getElementById(id+'role').innerHTML = '';
		return;
	}
	r = new XMLHttpRequest();
	r.onreadystatechange = function() {
		if (this.readyState === XMLHttpRequest.DONE) {
			if (this.status === 200) {
				msg = document.getElementById(id+'message');
				new_msg = msg.cloneNode(false);
				msg.parentNode.replaceChild(new_msg, msg);
				response = JSON.parse(this.responseText);
				if (response.Message != '')
					new_msg.appendChild(document.createElement('div')).innerHTML = response.Message;
				role = document.getElementById(id+'role');
				new_role = role.cloneNode(false);
				old_role = role.hasChildNodes() && 'value' in role.firstChild ? role.firstChild.value : role.textContent;
				select = new_role.appendChild(document.createElement("select"))
				select.name = 'primary_role';
				select.appendChild(document.createElement("option"))
				for (i = 0; i < response.Roles.length; i++) {
					option = select.appendChild(document.createElement("option"))
					option.value = option.innerHTML = response.Roles[i];
					if (option.value === old_role)
						option.selected = true;
				}
				role.parentNode.replaceChild(new_role, role);
				if (old_role != '' && select.value != old_role)
					new_msg.appendChild(document.createElement('div')).innerHTML = "Couldn't find role '"+old_role+"' in environment '"+environment+"'";

			} else {
				document.getElementById(id+'message').innerHTML = 'Problem: ' + r.responseText;
			}
		}
	}
	r.open('GET', '?action=getRoles&environment='+environment);
	r.send();
}
function addInput(form, name, value) {
	input = form.appendChild(document.createElement('input'));
	input.style.display = 'none';
	input.name = name;
	input.value = value;
}
function confirmSubmit(action, id) {
	environment_select = document.getElementById(id+'environment').firstChild;
	environment = environment_select.value;
	if (environment === '') {
		document.getElementById(id+'message').innerHTML = 'Environment is required.';
		environment_select.focus();
		return;
	}
	tr = document.getElementById(id+'tr');
	cert = tr.getAttribute('data-cert');
	if (!confirm('Update classification for '+cert+'?'))
		return;
	form = document.body.appendChild(document.createElement('form'));
	form.method = 'POST';
	form.action = '%s'
	addInput(form, 'action', action);
	addInput(form, 'cert', cert);
	addInput(form, 'environment', environment);
	addInput(form, 'primary_role', document.getElementById(id+'role').firstChild.value);
	form.submit();
}
function confirmSign(id) {
	tr = document.getElementById(id+'tr');
	cert = tr.getAttribute('data-cert');
	if (!confirm('Sign certificate for '+cert+'?'))
		return;
	form = document.body.appendChild(document.createElement('form'));
	form.method = 'POST';
	form.action = '%s';
	addInput(form, 'action', 'sign');
	addInput(form, 'cert', cert);
	form.submit();
}
</script>
</head>
<body>
`, nodeconfig_path, nodeconfig_path)
	if message != "" {
		fmt.Fprintf(response, "<div id=\"message\">%s</div>\n", message)
	}
	fmt.Fprintf(response, "<div><a href=\"?\">Refresh</a></div>\n")
	fmt.Fprintf(response, "<h3>Certificate Signing Requests</h3>\n")
	files, err := ioutil.ReadDir(ctx.appConfig.PuppetConfig.CsrDir)
	if err != nil {
		fmt.Fprintf(response, "Failed to read CsrDir '%s': %s<br/>\n", ctx.appConfig.PuppetConfig.CsrDir, err)
	} else {
		fmt.Fprintf(response, `
<table cellspacing="0" cellpadding="0" width="100%%">
	<tr>
		<th>Certificate Name</th>
		<th>Request Date</th>
		<th>Environment</th>
		<th>Primary Role</th>
		<th style="min-width:200px;">Message</th>
		<th>Actions</th>
	</tr>
`)
		for i, file := range files {
			if strings.HasSuffix(file.Name(), ".pem") {
				csrcert := strings.TrimSuffix(file.Name(), ".pem")
				config, _ := ctx.nodeClassifier.GetClassification(csrcert, true)
				signingDisabled := " disabled"
				if config.Environment != "" {
					signingDisabled = ""
				}
				fmt.Fprintf(response, `
	<tr id="csr%dtr" data-cert="%s">
		<td>%s</td>
		<td style="white-space: nowrap;">%s</td>
		<td id="csr%denvironment">%s</td>
		<td id="csr%drole">%s</td>
		<td id="csr%dmessage"></td>
		<td style="white-space: nowrap;">
			<button id="csr%dclassify" onclick="getEnvironments('csr%d');"/>Edit Classification</button>
			<button style="display:none;" id="csr%dsubmit" onclick="confirmSubmit('classifycsr', 'csr%d');"/>Update Classification</button>
			<button onclick="confirmSign('csr%d');"%s/>Sign</button>
		</td>
	</tr>
`,
					i, csrcert, //TR
					csrcert,                              //Certificate Name
					file.ModTime().Format(time.RubyDate), //Request Date
					i, config.Environment,                //Environment
					i, config.PrimaryRole, //Primary Role
					i,    //Message
					i, i, //Classify
					i, i, //Submit
					i, signingDisabled) //Sign
			}
		}
		fmt.Fprintf(response, `</table>`)
	}

	fmt.Fprintf(response, `<h3>Nodes</h3>`)
	files, err = ioutil.ReadDir(ctx.appConfig.NodesDir)
	if err != nil {
		fmt.Fprintf(response, "Failed to read nodes dir '%s': %s<br/>\n", ctx.appConfig.NodesDir, err)
	} else {
		fmt.Fprintf(response, `<table cellspacing="0" cellpadding="0">
	<tr><th>Node</th><th>Environment</th><th>Primary Role</th><th>Message</th><th>Actions</th></tr>
	<tr id="nodetr">
		<td>
			<select onchange="lookupNode(this.value);" name="cert">
				<option value=""></option>
`)
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".yaml") {
				certname := strings.TrimSuffix(file.Name(), ".yaml")
				fmt.Fprintf(response, "\t\t\t\t<option value=\"%s\">%s</option>\n", certname, certname)
			}
		}
		fmt.Fprintf(response, `			</select>
		</td>
		<td id="nodeenvironment"></td>
		<td id="noderole"></td>
		<td id="nodemessage"></td>
		<td>
			<button id="nodeclassify" onclick="getEnvironments('node');" disabled>Edit Classification</button>
			<button style="display:none;" id="nodesubmit" onclick="confirmSubmit('classify', 'node');">Update Classification</button>
		</td>
	</tr>
`)
	}
	fmt.Fprintf(response, "</table>\n</body>\n</html>")
}
