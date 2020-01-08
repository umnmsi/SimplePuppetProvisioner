package lib

import (
	"encoding/json"
	"fmt"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/certsign"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/genericexec"
	"gopkg.in/yaml.v2"
	"html"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type NodeConfigHandler struct {
	appConfig   *AppConfig
	notifier    *Notifications
	certSigner  *certsign.CertSigner
	execManager *genericexec.GenericExecManager
}

type NodeConfig struct {
	Environment string
	Parameters  struct {
		PrimaryRole string `yaml:"primary_role"`
	}
}

type EnvironmentsMsg struct {
	Status       string
	Message      string
	Environments []string
}

type RolesMsg struct {
	Status  string
	Message string
	Roles   []string
}

func NewNodeConfigHandler(appConfig *AppConfig, notifier *Notifications, certSigner *certsign.CertSigner, execManager *genericexec.GenericExecManager) *NodeConfigHandler {
	handler := NodeConfigHandler{appConfig: appConfig, notifier: notifier, certSigner: certSigner, execManager: execManager}
	return &handler
}

func (ctx NodeConfigHandler) GetNodeConfig(node string) (NodeConfig, string) {
	config := NodeConfig{}
	var message string
	source, err := ioutil.ReadFile("/home/nick/git/puppet/nodes/" + node + ".yaml")
	if err != nil {
		message = fmt.Sprintf("Failed to read YAML file for %s: %s", html.EscapeString(node), err)
	} else {
		err = yaml.Unmarshal(source, &config)
		if err != nil {
			message = fmt.Sprintf("Failed to parse YAML file for %s: %s", html.EscapeString(node), err)
		} else {
			message = fmt.Sprintf("Environment: %s, Primary Role: %s", config.Environment, config.Parameters.PrimaryRole)
		}
	}
	fmt.Printf("node '%s', config %#v, message '%s'\n", node, config, message)
	return config, message
}

func (ctx NodeConfigHandler) GetEnvironments() EnvironmentsMsg {
	var environments []string
	for _, path := range ctx.appConfig.PuppetConfig.EnvironmentPath {
		dirs, err := ioutil.ReadDir(path)
		if err != nil {
			return EnvironmentsMsg{"ERROR", fmt.Sprintf("Failed to read environment path %s: %s", path, err), environments}
		} else {
			for _, environment := range dirs {
				if !strings.HasPrefix(environment.Name(), ".") {
					environments = append(environments, environment.Name())
				}
			}
		}
	}
	return EnvironmentsMsg{"OK", "", environments}
}

func (ctx NodeConfigHandler) GetRoles(environment string) RolesMsg {
	roles := []string{}
	role_path := ""
PathLoop:
	for _, path := range ctx.appConfig.PuppetConfig.EnvironmentPath {
		dirs, err := ioutil.ReadDir(path)
		if err != nil {
			return RolesMsg{"ERROR", fmt.Sprintf("Failed to read environment path %s: %s", path, err), roles}
		} else {
			for _, dir := range dirs {
				if dir.Name() == environment {
					role_path = filepath.Join(path, dir.Name(), "site", "role", "manifests")
					break PathLoop
				}
			}
		}
	}
	if role_path == "" {
		return RolesMsg{"ERROR", fmt.Sprintf("Failed to find environment directory for '%s'", environment), roles}
	}
	files, err := ioutil.ReadDir(role_path)
	if err != nil {
		return RolesMsg{"ERROR", fmt.Sprintf("Failed to read role path %s: %s", role_path, err), roles}
	} else {
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".pp") {
				roles = append(roles, strings.TrimSuffix(file.Name(), ".pp"))
			}
		}
	}
	return RolesMsg{"OK", "", roles}
}

func (ctx NodeConfigHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	fmt.Printf("Request: %#v\n", request)
	allowed, err := regexp.Match(`^/nodeconfig($)`, []byte(request.URL.Path))
	if allowed {
		response.WriteHeader(http.StatusOK)
	} else {
		response.WriteHeader(http.StatusNotFound)
		return
	}

	action, cert, message := "", "", ""

	switch request.Method {
	case http.MethodGet:
		q := request.URL.Query()
		fmt.Printf("query: %#v\n", q)
		if q["action"] != nil {
			action = q["action"][0]
		}
		switch action {
		case "":
		case "getEnvironments":
			environments, err := json.Marshal(ctx.GetEnvironments())
			if err != nil {
				fmt.Fprintf(response, `{"Status":"ERROR","Message":"Failed to encode JSON: `+err.Error()+`"}`)
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
			roles, err := json.Marshal(ctx.GetRoles(environment))
			if err != nil {
				fmt.Fprintf(response, `{"Status":"ERROR","Message":"Failed to encode JSON: `+err.Error()+`"}`)
				return
			}
			response.Write(roles)
			return
		default:
			message = fmt.Sprintf("Invalid action '%s'", html.EscapeString(action))
		}
	case http.MethodPost:
		request.ParseForm()
		action = request.Form.Get("action")
		switch action {
		case "sign":
			cert = request.Form.Get("cert")
			if cert == "" {
				message = "Missing 'cert' argument for action 'sign'"
				action = ""
			} else if false {
				signingResultChan := ctx.certSigner.Sign(cert, false)
				select {
				case res := <-signingResultChan:
					if res.Success {
						message = fmt.Sprintf("Signing completed for %s: %s", cert, res.Message)
					} else {
						message = fmt.Sprintf("Signing failed for %s: %s", cert, res.Message)
					}
				case <-time.After(60 * time.Second):
					message = "Timeout waiting for signing"
				}
			}
		case "classify":
			fallthrough
		case "classifycsr":
			cert = request.Form.Get("cert")
			if cert == "" {
				message = fmt.Sprintf("Missing 'cert' argument for action '%s'", html.EscapeString(action))
				action = ""
			} else {
			}
		default:
			message = fmt.Sprintf("Invalid action '%s'", html.EscapeString(action))
		}
	default:
		message = fmt.Sprintf("Invalid method '%s'", html.EscapeString(request.Method))
	}

	fmt.Fprintf(response, `
<html>
<head>
<style type="text/css">
body { font-family: arial; }
form { margin-block-end: 0; }
th { padding: 0 5px; text-align: left; }
td { padding: 0 5px; min-width: 100px; }
tr:hover { background-color: #F0F0F0; }
</style>
<script type="text/javascript">
function getEnvironments(id) {
	console.log("getEnvironments", id);
	r = new XMLHttpRequest();
	r.onreadystatechange = function() {
		console.log("getEnvironments response", id, this.readyState);
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
				console.log("old env: " + old_env);
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
	console.log('getRoles', arguments);
	if (environment === '') {
		document.getElementById(id+'role').innerHTML = '';
		return;
	}
	r = new XMLHttpRequest();
	r.onreadystatechange = function() {
		console.log("getRoles response", id, this.readyState);
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
				console.log("old role: " + old_role);
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
	input.name = name;
	input.value = value;
}
function confirmSubmit(id) {
	console.log('confirmSubmit', id)
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
	addInput(form, 'action', 'classifycsr');
	addInput(form, 'cert', cert);
	addInput(form, 'environment', environment);
	addInput(form, 'primary_role', document.getElementById(id+'role').firstChild.value);
	form.submit();
}
function confirmSign(id) {
	console.log('confirmSign', id);
	tr = document.getElementById(id+'tr');
	cert = tr.getAttribute('data-cert');
	if (!confirm('Sign certificate for '+cert+'?'))
		return;
	form = document.body.appendChild(document.createElement('form'));
	form.method = 'POST';
	addInput(form, 'action', 'sign');
	addInput(form, 'cert', cert);
	form.submit();
}
</script>
</head>
<body>
`)
	if message != "" {
		fmt.Fprintf(response, "<div>%s</div>", message)
	}

	fmt.Fprintf(response, `<h2>Certificate Signing Requests</h2>`)
	files, err := ioutil.ReadDir(ctx.appConfig.PuppetConfig.CsrDir)
	if err != nil {
		fmt.Fprintf(response, "Failed to read CsrDir: %s<br/>\n", err)
	} else {
		/*
			fmt.Fprintf(response, `<form method="POST"><input type="hidden" name="action" value="sign"/><select name="cert"><option value=""></option>`)
			for _, file := range files {
				if strings.HasSuffix(file.Name(), ".pem") {
					csrcert := strings.TrimSuffix(file.Name(), ".pem")
					selected := ""
					if action == "sign" && cert == csrcert {
						selected = " selected"
					}
					fmt.Fprintf(response, "<option value=\"%s\"%s>%s</option>\n", csrcert, selected, csrcert)
				}
			}
		*/
		fmt.Fprintf(response, `
<table cellspacing="0" cellpadding="0"
	<tr>
		<th>Certificate Name</th>
		<th>Request Date</th>
		<th>Environment</th>
		<th>Primary Role</th>
		<th>Message</th>
		<th></th>
		<th></th>
	</tr>
`)
		for i, file := range files {
			if strings.HasSuffix(file.Name(), ".pem") {
				csrcert := strings.TrimSuffix(file.Name(), ".pem")
				config, _ := ctx.GetNodeConfig(csrcert)
				signingDisabled := " disabled"
				if config.Environment != "" {
					signingDisabled = ""
				}
				fmt.Fprintf(response, `
	<tr id="csr%dtr" data-cert="%s">
		<td>%s</td>
		<td>%s</td>
		<td id="csr%denvironment">%s</td>
		<td id="csr%drole">%s</td>
		<td id="csr%dmessage"></td>
		<td>
			<button id="csr%dclassify" onclick="getEnvironments('csr%d');"/>Edit Classification</button>
			<button style="display:none;" id="csr%dsubmit" onclick="confirmSubmit('csr%d');"/>Submit</button>
		</td>
		<td>
			<button onclick="confirmSign('csr%d');"%s/>Sign</button>
		</td>
	</tr>
`,
					i, csrcert, //TR data
					csrcert,                              //Certificate Name
					file.ModTime().Format(time.RubyDate), //Request Date
					i, config.Environment,                //Environment
					i, config.Parameters.PrimaryRole, //Primary Role
					i,    //Message
					i, i, //Classify
					i, i, //Submit
					i, signingDisabled) //Sign
			}
		}
		fmt.Fprintf(response, `</table>`)
	}

	fmt.Fprintf(response, `<h2>Nodes</h2>`)
	files, err = ioutil.ReadDir("/home/nick/git/puppet/nodes")
	if err != nil {
		fmt.Fprintf(response, "Failed to read nodes dir: %s<br/>\n", err)
	} else {
		fmt.Fprintf(response, `<form method="POST"><input type="hidden" name="action" value="classify"/><select name="cert"><option value=""></option>`)
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".yaml") {
				certname := strings.TrimSuffix(file.Name(), ".yaml")
				selected := ""
				if action == "classify" && cert == certname {
					selected = " selected"
				}
				fmt.Fprintf(response, "<option value=\"%s\"%s>%s</option>\n", certname, selected, certname)
			}
		}
	}
	fmt.Fprintf(response, "</select>")
	if action == "classify" {
		_, message := ctx.GetNodeConfig(cert)
		fmt.Fprintf(response, message)
		fmt.Fprintf(response, `<input type="submit" value="Update classification"/>`+"\n")
	} else {
		fmt.Fprintf(response, `<input type="submit" value="Lookup classification"/>`+"\n")
	}
	fmt.Fprintf(response, "</form>\n")

	fmt.Fprintf(response, "</body></html>")
}
